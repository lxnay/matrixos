package releaser

import (
	"fmt"
	"io"
)

func (r *Releaser) SetStdout(w io.Writer) {
	r.ioMu.Lock()
	defer r.ioMu.Unlock()
	r.stdout = w
}
func (r *Releaser) SetStderr(w io.Writer) {
	r.ioMu.Lock()
	defer r.ioMu.Unlock()
	r.stderr = w
}
func (r *Releaser) Stdout() io.Writer {
	r.ioMu.Lock()
	defer r.ioMu.Unlock()
	return r.stdout
}
func (r *Releaser) Stderr() io.Writer {
	r.ioMu.Lock()
	defer r.ioMu.Unlock()
	return r.stderr
}

func (r *Releaser) SetChrootDir(dir string) { r.chrootDir = dir }
func (r *Releaser) ChrootDir() string       { return r.chrootDir }

func (r *Releaser) SetImageDir(dir string) error {
	if err := checkImageDir(dir); err != nil {
		return err
	}
	r.imageDir = dir
	return nil
}

func (r *Releaser) ImageDir() string { return r.imageDir }

func (r *Releaser) SetRef(ref string) { r.ref = ref }
func (r *Releaser) Ref() string       { return r.ref }

func (r *Releaser) Print(format string, args ...any) {
	r.ioMu.Lock()
	defer r.ioMu.Unlock()
	fmt.Fprintf(r.stdout, format, args...)
}

func (r *Releaser) PrintWarning(format string, args ...any) {
	r.ioMu.Lock()
	defer r.ioMu.Unlock()
	fmt.Fprintf(r.stderr, format, args...)
}

func (r *Releaser) PrintError(format string, args ...any) {
	r.ioMu.Lock()
	defer r.ioMu.Unlock()
	fmt.Fprintf(r.stderr, format, args...)
}
