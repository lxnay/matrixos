package seeder

import (
	"bytes"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
)

// newTestSeeder returns a Seeder with mock dependencies suitable for unit tests.
func newTestSeeder() *Seeder {
	mr := runner.NewMockRunner()
	cfg := &config.MockConfig{Items: map[string][]string{}, Bools: map[string]bool{}}
	s := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	s.SetStdout(&bytes.Buffer{})
	s.SetStderr(&bytes.Buffer{})
	return s
}
