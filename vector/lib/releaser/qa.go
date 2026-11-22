package releaser

func (r *Releaser) CheckMatrixOS() error {
	return r.qa.CheckMatrixOSPrivate()
}

func (r *Releaser) PreCleanQAChecks() error {
	if err := checkImageDir(r.imageDir); err != nil {
		return err
	}
	r.qa.SetStderr(r.Stderr())
	r.Print("Pre clean QA Checks ...\n")

	sbCertPath, err := r.SecureBootCertPath()
	if err != nil {
		return err
	}

	if err := r.qa.VerifyDistroRootfsEnvironmentSetup(r.imageDir); err != nil {
		return err
	}
	if err := r.qa.CheckSecureBoot(r.imageDir, sbCertPath); err != nil {
		return err
	}
	if err := r.qa.CheckNumberOfKernels(r.imageDir, 1); err != nil {
		return err
	}

	r.Print("Pre clean QA Checks complete\n")
	return nil
}
