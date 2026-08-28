package state

import (
	"fmt"
	"sync"
	"testing"
)

// Two wyrm processes starting different projects at the same moment must not
// erase each other's record: the loser's MarkStarted used to vanish, and
// on_project_first_start then fired a second time for a project that had
// already started.
func TestConcurrentMarkStartedKeepsEveryEntry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	const writers = 12
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := Load() // each "process" loads its own view, as wyrm does
			if err != nil {
				errs <- err
				return
			}
			if err := s.MarkStarted(fmt.Sprintf("/project/%02d", i)); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	final, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < writers; i++ {
		dir := fmt.Sprintf("/project/%02d", i)
		if !final.Started(dir) {
			t.Errorf("%s was lost: %d of %d entries survived", dir, final.Len(), writers)
		}
	}
}
