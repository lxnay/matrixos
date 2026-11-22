package runner

import "sync"

// MockRunnerCall records a single command invocation.
type MockRunnerCall struct {
	Name      string
	Args      []string
	Dir       string
	Env       []string
	ChrootDir string // set only by ChrootRun
}

// MockRunner records calls and returns configurable errors.
// Use NewMockRunner for a runner that always succeeds, or
// NewMockRunnerFailOnCall to fail on a specific invocation index.
type MockRunner struct {
	mu     sync.Mutex
	Calls  []MockRunnerCall
	Err    error
	FailOn int // Fail on this call index (0-based), -1 means always fail if Err != nil
}

func (mr *MockRunner) recordCmd(c *Cmd) {
	mr.Calls = append(mr.Calls, MockRunnerCall{
		Name: c.Name, Args: c.Args, Dir: c.Dir, Env: c.Env,
	})
}

func (mr *MockRunner) recordChrootCmd(c *ChrootCmd) {
	mr.Calls = append(mr.Calls, MockRunnerCall{
		Name: "chroot:" + c.Name, Args: c.Args,
		Dir: c.Dir, Env: c.Env, ChrootDir: c.ChrootDir,
	})
}

// Run implements the Func signature.
func (mr *MockRunner) Run(c *Cmd) error {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	mr.recordCmd(c)
	return mr.errForCall()
}

// errForCall returns the error for the current call index, if any.
func (mr *MockRunner) errForCall() error {
	idx := len(mr.Calls) - 1
	if mr.FailOn >= 0 && idx == mr.FailOn {
		return mr.Err
	}
	if mr.FailOn < 0 && mr.Err != nil {
		return mr.Err
	}
	return nil
}

// ChrootRun implements the ChrootRunFunc signature.
func (mr *MockRunner) ChrootRun(c *ChrootCmd) error {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	mr.recordChrootCmd(c)
	return mr.errForCall()
}

// NewMockRunner creates a MockRunner that always succeeds.
func NewMockRunner() *MockRunner {
	return &MockRunner{FailOn: -1}
}

// NewMockRunnerFailOnCall creates a MockRunner that returns err on the n-th
// call (0-based) and succeeds on all others.
func NewMockRunnerFailOnCall(n int, err error) *MockRunner {
	return &MockRunner{FailOn: n, Err: err}
}
