# 音效触发延迟测量口径与验收规范

本文档固化音效触发链路的延迟测量口径、统计方式、达标线、并发与降级要求，
并给出与 `soundfx` 包实现的一一对应关系。代码入口：`soundfx/pool.go`，
可运行的测量程序：`cmd/soundfx-bench/main.go`。

## 1. 测量对象与时间戳定义

一次触发产生一个延迟样本，两个时间戳：

- `t0`：调用触发接口（`Pool.Trigger`）进入函数的时刻打点。
  实现在 `soundfx/pool.go` 的 `Trigger` 首行 `p.clock.Now()`。
- `t1`：该音效的音频输出首帧到达的时刻打点。定义为音频泵第一次从该路
  音源拉取出非空采样块的时刻（首帧进入输出路径）。实现在
  `soundfx/probe.go` 的 `firstFrameProbe.Stream`，仅记录一次。
- 单样本延迟：`latency = t1 - t0`。

口径说明：

- `t1` 测量点在“进入输出 Sink 的首帧”，即混音输出路径的首帧；在真机上把
  `MemorySink` 换成 oto 后端 Sink 后，同一时间戳位置即对应写入设备缓冲区
  的首帧。设备自身的硬件缓冲延迟属于输出链路固定开销，需通过调小
  `BlockSamples`/设备缓冲控制，不与触发调度延迟混算；如需端到扬声器口径，
  另加一项“设备缓冲延迟”单独披露。
- 打点时钟使用单调时钟（Go `time.Now` 内部基于 monotonic clock），不受墙
  钟回拨影响；测试中用 `FakeClock` 保证结果可复现。

## 2. 统计口径与达标线

- 样本量：每次有效测量不少于 **1000** 次触发（常量 `soundfx.MinSamples`）。
  样本不足时 `Snapshot()` 直接判定不达标，不允许外推。
- 统计量：**中位数 p50** 与 **九十九分位 p99**，采用 nearest-rank（向上取
  整秩）方法，不做插值，定义见 `soundfx/metrics.go` 的 `Percentile`。
- 达标线（严格小于，边界值不通过）：
  - 中位数 `< 20 ms`（`soundfx.MedianLimit`）。
  - p99 `< 35 ms`（`soundfx.P99Limit`）。
- 判定函数：`Metrics.Snapshot()` 返回 `Pass`，条件为
  `Samples >= 1000 && Median < 20ms && P99 < 35ms`。
- 测量期间必须同时记录：拒绝数 `Rejected`、抢占数 `Stolen`、削顶采样数
  `Clamped`，任一非预期增长视为本次测量无效或不达标。

复现方式：

```
go test ./soundfx/
go run ./cmd/soundfx-bench -samples 1000 -voices 32
```

## 3. 并发要求：32 路音效不丢、不爆

- 池容量 `MaxVoices = 32`，32 路并发触发必须全部拿到声部并各自产出首帧，
  `Rejected == 0`，首帧样本数 == 32（测试 `Test32ConcurrentNoDropNoClip`）。
- “不丢”：预加载命中的触发在容量内不允许失败；泵按固定块长混音，所有在
  播放声部都会进入 `Mixer`，声部自然播完才回收。
- “不爆”：混音结果必须落在 `[-1, 1]`，`MemorySink.Clamped` 为 0。工程约束：
  单路音源按 `1/MaxVoices` 做响度预算（32 路满幅叠加的上界恰为满刻度）；
  接入真实素材时在预加载阶段做归一化，不允许在触发热路径上做动态压缩。
- 触发线程与音频泵线程之间通过无等待队列传递新声部（`pendingMu` 保护的
  切片），触发方不在混音锁上阻塞。

## 4. 池满时的降级策略

池满（在播放声部数达到 `MaxVoices`）时，触发不允许阻塞等待，二选一，由
`PoolConfig.Policy` 显式配置：

1. `PolicyReject`（默认，保正在播放的声音）
   - 新触发立即返回 `ErrPoolExhausted`，`Rejected` 计数加一。
   - 适用场景：脚步、UI 反馈等可丢弃音效；调用方可以选择静默或合并触发。
2. `PolicyStealOldest`（保最新触发）
   - 停止最早触发（播放时间最长）的声部，立即静音并回收槽位给新音效，
     `Stolen` 计数加一。
   - 选择最旧声部的依据是触发时单调递增的 ID（`nextOldestLocked`）。
   - 适用场景：命中反馈、技能音效等“最新事件优先”的玩法。

两种策略都保证 `Trigger` 调用本身不等待音频线程，也不做内存分配以外的
I/O。计数必须上报监控，降级率持续走高时应回溯声部预算与素材时长。

## 5. 循环音效停止：1 ms 内静音、无尾音

- 循环通过 `beep.Loop(-1, src)` 实现，外部用 `StopGate` 包裹。
- `TriggerHandle.Stop()` 是无锁原子操作（`atomic.StoreUint32`），可在任意
  线程调用。
- 静音时限由泵的块长保证：`BlockSamples` 对应的时长必须 `< 1 ms`。参考配置
  48 kHz、32 帧/块 = 0.667 ms（`Pool.BlockDuration()`）。
- 停止后的行为：
  - 停止之后新拉取的块直接返回 drained，输出全零，不存在尾音。
  - 若 `Stop` 与正在进行的拉取竞态，本块在最多 `maxFadeSamples = 24` 帧
    （0.5 ms @48k）内线性淡出并清零剩余采样，既无截断爆音，也保证从
    `Stop()` 到实际静音 `< 1 ms`。
- 验收测试：`TestLoopStopMutedWithinOneMillisecond`、
  `TestStopGateFadesRacingBlock` 校验停止后所有输出帧为零、淡出单调且
  块长严格小于 1 ms。

## 6. 线程模型与预加载时机

- 触发路径禁止阻塞主线程：`Trigger` 只做预加载缓冲查表、构造轻量
  streamer、入队三个动作；不做解码、不做文件/网络 I/O、不等待泵线程。
  测试 `TestTriggerDoesNotBlockOnPump` 在泵完全不运转时验证 32 次触发
  仍可在超时内完成。
- 预加载放在场景切换阶段：`Pool.Preload`/`PreloadAsync` 在场景加载时把素材
  全部解码进 `beep.Buffer`（内存 PCM），场景激活前 `Wait` 完成；旧场景素材
  用 `Unload` 释放。触发期只从只读 Buffer 派生新的 `StreamSeeker`，零解码成本。
- 音频泵独占混音与设备写入：`StartPump=true` 时内部 goroutine 按
  `BlockDuration` 定 tick 驱动；无头测试/基准用 `Step()` 手动驱动，保证时间
  测量确定性。

## 7. 真机接入注意事项

- 用 oto 实现 `Sink` 接口（`WriteBlock` 内做 int16 转换与 `player.Write`），
  设备缓冲帧数与 `BlockSamples` 对齐；48 kHz 下建议设备缓冲不超过 2 个块，
  避免硬件缓冲把 p99 顶过 35 ms。
- 正式测量在目标机型、目标帧率负载下进行；本仓内的 `soundfx-bench` 用于
  CI 回归调度逻辑与统计口径，真机延迟以同一套 `Metrics` 在设备上采集为准。
- 任何把解码、重采样放进触发路径的改动都属于违反本规范，评审时直接打回。
