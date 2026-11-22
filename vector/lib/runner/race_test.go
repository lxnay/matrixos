package runner

import (
	"sync"
	"testing"
)

func TestMockRunner_ConcurrentRun(t *testing.T) {
	mr := NewMockRunner()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_ = mr.Run(&Cmd{Name: "echo", Args: []string{"hi"}})
				_, _ = mr.Output(&Cmd{Name: "echo"})
				_ = mr.ChrootRun(&ChrootCmd{Cmd: Cmd{Name: "sh"}, ChrootDir: "/tmp"})
			}
		}()
	}
	wg.Wait()
	if len(mr.Calls) != 8*50*3 {
		t.Errorf("got %d calls, want %d", len(mr.Calls), 8*50*3)
	}
}
