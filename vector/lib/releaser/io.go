package releaser

import "io"

// SetStderr replaces the writer used for warnings and errors and propagates
// it to the embedded QA validator.
func (r *Releaser) SetStderr(w io.Writer) {
	r.IO.SetStderr(w)
	if r.qa != nil {
		r.qa.SetStderr(w)
	}
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
