package soundfx

import "fmt"

func errEmptyEffect(name string) error {
	return fmt.Errorf("soundfx: preloaded effect %q produced no samples", name)
}
