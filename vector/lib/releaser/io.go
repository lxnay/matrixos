package releaser

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
