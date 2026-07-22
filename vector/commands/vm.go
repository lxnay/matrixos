package commands

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"matrixos/vector/lib/filesystems"
)

var goArchToQemu = map[string]string{
	"amd64": "x86_64",
	"arm64": "aarch64",
}

func qemuSystemBinary() string {
	arch, ok := goArchToQemu[runtime.GOARCH]
	if !ok {
		arch = runtime.GOARCH
	}
	return "qemu-system-" + arch
}

const (
	rootPassword = "matrix"
)

type VMDriver struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	reader  *bufio.Reader
	display io.Writer // where to echo VM output (nil = discard)
}

func NewVMDriver(ctx context.Context, args []string) (*VMDriver, error) {
	cmd := exec.CommandContext(ctx, qemuSystemBinary(), args...)
	cmd.Stderr = os.Stderr

	cmd.Cancel = func() error {
		// Do not send SIGKILL immediately when canceling ctx.
		return cmd.Process.Signal(os.Interrupt)
	}
	// Wait 30s before sending SIGKILL.
	cmd.WaitDelay = 30 * time.Second

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	return &VMDriver{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		reader:  bufio.NewReader(stdout),
		display: os.Stdout,
	}, nil
}

// Start starts the VM process
func (vm *VMDriver) Start() error {
	return vm.cmd.Start()
}

// Expect waits for a specific string in the output within a timeout
func (vm *VMDriver) Expect(target string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resultCh := make(chan error)

	go func() {
		buf := make([]byte, 1024)
		var matchBuf strings.Builder
		for {
			n, err := vm.reader.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				if vm.display != nil {
					fmt.Fprint(vm.display, chunk)
				}
				matchBuf.WriteString(chunk)
				if strings.Contains(matchBuf.String(), target) {
					resultCh <- nil
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					resultCh <- err
				} else {
					// EOF reached but target not found yet, loop will exit next read
					resultCh <- fmt.Errorf("EOF reached while waiting for pattern: %q", target)
				}
				return
			}
		}
	}()

	select {
	case err := <-resultCh:
		return err
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for pattern: %q", target)
	}
}

// Send writes a command to the VM
func (vm *VMDriver) Send(cmd string) error {
	_, err := fmt.Fprintf(vm.stdin, "%s\n", cmd)
	return err
}

func (vm *VMDriver) Close() error {
	return vm.Wait()
}

// Wait waits for the VM process to exit
func (vm *VMDriver) Wait() error {
	return vm.cmd.Wait()
}

// VMCommand checks matrixOS images via QEMU
type VMCommand struct {
	BaseCommand
	UI
	fs            *flag.FlagSet
	imagePath     string
	memory        string
	gpuMemory     string
	sshPort       string
	waitBoot      time.Duration
	waitTests     time.Duration
	maxRunTime    time.Duration
	display       string
	venusAccel    bool
	graphical     bool
	gpuAccel      bool
	audio         bool
	interactive   bool
	audioDev      string
	cpus          string
	extraDisk     bool
	extraDiskSize string

	// Styled I/O writers
	stdout *styledWriter
	stderr *styledWriter
}

// NewVMCommand creates a new VMCommand
func NewVMCommand() *VMCommand {
	c := &VMCommand{
		fs: flag.NewFlagSet("vm", flag.ExitOnError),
	}
	c.fs.StringVar(&c.imagePath, "image", "", "Path to the matrixOS image")
	c.fs.StringVar(&c.memory, "memory", "4G", "Amount of RAM for the VM")
	c.fs.StringVar(&c.gpuMemory, "gpu_memory", "512M", "Amount of memory for the virtual GPU")
	c.fs.StringVar(&c.audioDev, "audio_dev", "pipewire", "Audio device for the VM (default 'pipewire' for PipeWire)")
	c.fs.StringVar(&c.sshPort, "ssh_port", "", "Local port for SSH forwarding (set to enable)")
	c.fs.DurationVar(&c.waitBoot, "wait_boot", 120*time.Second, "Seconds to wait for boot login prompt")
	c.fs.DurationVar(&c.waitTests, "wait_tests", 120*time.Second, "Seconds to wait for tests to complete")
	c.fs.DurationVar(&c.maxRunTime, "max_run_time", 300*time.Second, "Maximum seconds to allow the entire VM run (including boot and tests), when running in non-interactive mode")
	c.fs.BoolVar(&c.venusAccel, "venus", false, "Enable Venus GPU acceleration (requires QEMU with Venus support)")
	c.fs.BoolVar(&c.gpuAccel, "gpuaccel", true, "Enable GPU acceleration (requires QEMU with GPU support)")
	c.fs.BoolVar(&c.graphical, "graphical", true, "Enable graphical output")
	c.fs.BoolVar(&c.audio, "audio", true, "Enable audio devices")
	c.fs.BoolVar(&c.interactive, "interactive", false, "Run VM interactively without testing")
	c.fs.StringVar(&c.cpus, "cpus", "4", "Number of CPUs for the VM")
	c.fs.StringVar(&c.display, "display", "sdl", "Display type for the VM (e.g., 'sdl', 'gtk', default: 'sdl')")
	c.fs.BoolVar(&c.extraDisk, "extra_disk", false, "Attach an additional empty block device to the VM")
	c.fs.StringVar(&c.extraDiskSize, "extra_disk_size", "64G", "Size of the extra block device (default '64G')")
	return c
}

func (c *VMCommand) Name() string {
	return c.fs.Name()
}

func (c *VMCommand) Init(args []string) error {
	if err := c.initBaseConfig(); err != nil {
		return err
	}
	c.fs.Usage = func() {
		fmt.Printf("Usage: vector dev %s\n", c.Name())
		c.fs.PrintDefaults()
	}
	if err := c.fs.Parse(args); err != nil {
		return err
	}

	c.StartUI()

	return nil
}

func (c *VMCommand) Run() error {
	if c.imagePath == "" {
		c.fs.Usage()
		return fmt.Errorf("missing required flag: --image")
	}

	// Set up styled writers for output.
	c.stdout = c.NewStdoutWriter("vm")
	c.stderr = c.NewStderrWriter("vm")
	c.SetupPrinters("vm")
	defer c.FlushPrinters()
	defer c.stdout.Flush()
	defer c.stderr.Flush()

	tempDir, err := os.MkdirTemp("", "matrixos-vm-")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	mroot, err := c.cfg.GetItem("matrixOS.Root")
	if err != nil {
		return fmt.Errorf("failed to get matrixOS.Root from config: %w", err)
	}

	codeSrc := filepath.Join(mroot, "vector/tests/data/OVMF_CODE.fd")
	codeDst := filepath.Join(tempDir, "OVMF_CODE.fd")
	if err := filesystems.CopyFile(codeSrc, codeDst); err != nil {
		return fmt.Errorf("failed to copy OVMF_CODE.fd: %w", err)
	}

	varsSrc := filepath.Join(mroot, "vector/tests/data/OVMF_VARS.fd")
	varsDst := filepath.Join(tempDir, "OVMF_VARS.fd")
	if err := filesystems.CopyFile(varsSrc, varsDst); err != nil {
		return fmt.Errorf("failed to copy OVMF_VARS.fd: %w", err)
	}

	// Generate an ext4 filesystem from vector/tests/vm-suite to inject into the VM for testing.
	// This allows us to easily add test scripts and data without modifying the image build process.
	testImageFile, err := os.CreateTemp("", "vm-suite-*.img")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(testImageFile.Name())
	truncCmd := exec.Command("truncate", "-s", "64M", testImageFile.Name())
	if err := truncCmd.Run(); err != nil {
		return fmt.Errorf("failed to truncate test image: %w", err)
	}
	testImageFile.Close()

	mkfsTestImgArgs := []string{
		"mkfs.ext4",
		"-L", "TESTDATA",
		"-F",
		"-d", "tests/vm-suite",
		testImageFile.Name(),
	}
	c.Printf("Generating test filesystem with command: %v\n", mkfsTestImgArgs)
	if err := exec.Command(mkfsTestImgArgs[0], mkfsTestImgArgs[1:]...).Run(); err != nil {
		return fmt.Errorf("failed to create test image filesystem: %w", err)
	}

	// Optionally create an extra block device.
	var extraDiskFile *os.File
	if c.extraDisk {
		var err error
		extraDiskFile, err = os.CreateTemp("", "vm-extra-disk-*.img")
		if err != nil {
			return fmt.Errorf("failed to create extra disk temp file: %w", err)
		}
		defer os.Remove(extraDiskFile.Name())
		extraDiskFile.Close()

		truncExtraCmd := exec.Command("truncate", "-s", c.extraDiskSize, extraDiskFile.Name())
		if out, err := truncExtraCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to truncate extra disk to %s: %s: %w", c.extraDiskSize, string(out), err)
		}
		c.Printf("Created extra block device: %s (%s)\n", extraDiskFile.Name(), c.extraDiskSize)
	}

	format := "raw"
	if strings.HasSuffix(c.imagePath, ".qcow2") {
		format = "qcow2"
	}

	nicArg := "user,model=virtio-net-pci"
	if c.sshPort != "" {
		nicArg += fmt.Sprintf(",hostfwd=tcp::%s-:22", c.sshPort)
	}

	qemuArgs := []string{
		"-enable-kvm", "-m", c.memory,
		"-cpu", "host", "-smp", c.cpus,
		"-nic", nicArg,
		"-drive", "if=pflash,format=raw,readonly=on,file=" + codeDst,
		"-drive", "if=pflash,format=raw,file=" + varsDst,
		"-drive", fmt.Sprintf("file=%s,format=%s,if=virtio", c.imagePath, format),
		"-drive", fmt.Sprintf("file=%s,format=raw,if=virtio", testImageFile.Name()),
	}

	if extraDiskFile != nil {
		qemuArgs = append(qemuArgs,
			"-drive", fmt.Sprintf("file=%s,format=raw,if=virtio", extraDiskFile.Name()),
		)
	}

	if c.graphical {
		qemuArgs = append(qemuArgs, "-serial", "stdio")
		qemuArgs = append(qemuArgs, c.displayArgs()...)
	} else {
		qemuArgs = append(qemuArgs, "-nographic")
	}

	if !c.interactive {
		// Override SMBIOS manufacturer so grub.cfg enables serial console.
		// See image/boot/*/*/*/grub.cfg
		qemuArgs = append(qemuArgs, "-smbios", "type=1,manufacturer=matrixos-testmode-serial")
	}

	if c.audio {
		qemuArgs = append(qemuArgs,
			"-audiodev", fmt.Sprintf("%s,id=snd0", c.audioDev),
			"-device", "intel-hda",
			"-device", "hda-duplex,audiodev=snd0",
		)
	}

	c.Printf("QEMU args: %v\n", qemuArgs)
	if c.interactive {
		return c.runInteractive(qemuArgs)
	} else {
		return c.runTests(qemuArgs)
	}
}

func (c *VMCommand) displayArgs() []string {
	var qemuArgs []string

	if !c.gpuAccel {
		c.Printf("GPU acceleration disabled, using basic VGA display\n")
		qemuArgs = append(qemuArgs,
			"-device", "virtio-vga,hostmem="+c.gpuMemory,
			"-display", c.display+",gl=off",
		)
		return qemuArgs
	}

	venusAccel := ""
	if c.venusAccel {
		venusAccel = ",venus=on"
	}
	glOn := ",gl=on"

	qemuArgs = append(qemuArgs,
		"-device", "virtio-vga-gl,hostmem="+c.gpuMemory+",blob=true"+venusAccel,
		"-display", c.display+glOn,
	)
	return qemuArgs
}

func (c *VMCommand) runInteractive(qemuArgs []string) error {
	c.Println("Starting VM in interactive mode...")
	vm, err := NewVMDriver(context.Background(), qemuArgs)
	if err != nil {
		return fmt.Errorf("failed to init VM: %w", err)
	}
	vm.display = os.Stdout
	defer vm.Close()

	if err := vm.Start(); err != nil {
		return fmt.Errorf("failed to start VM: %w", err)
	}
	if err := vm.Wait(); err != nil {
		return fmt.Errorf("VM exited with error: %w", err)
	}
	return nil
}

// budgetRemaining returns how much time is left within budget given start,
// capped to cap. The result may be negative (budget already expired).
func budgetRemaining(start time.Time, budget, cap time.Duration) time.Duration {
	return min(budget-time.Since(start), cap)
}

func (c *VMCommand) runTests(qemuArgs []string) error {
	c.Println("Starting VM Test...")
	// How long do we allow the whole test suite to run?
	ctx, cancel := context.WithTimeout(context.Background(), c.maxRunTime)
	defer cancel()

	vm, err := NewVMDriver(ctx, qemuArgs)
	if err != nil {
		return fmt.Errorf("failed to init VM: %w", err)
	}
	vm.display = os.Stdout
	defer vm.Close()

	if err := vm.Start(); err != nil {
		return fmt.Errorf("failed to start VM: %w", err)
	}

	startTime := time.Now()

	loginRemainingTime := budgetRemaining(startTime, c.maxRunTime, c.waitBoot)
	if loginRemainingTime <= 0 {
		return fmt.Errorf("no time left to wait for VM boot: %v", loginRemainingTime)
	}

	c.Printf("Waiting for login prompt. Remaining budget: %v\n", loginRemainingTime)
	if err := vm.Expect("matrixos login:", loginRemainingTime); err != nil {
		return fmt.Errorf("boot failed: %w", err)
	}

	c.Println("Logging in...")
	if err := vm.Send("root"); err != nil {
		return err
	}

	remainingPasswordTime := budgetRemaining(startTime, c.maxRunTime, 5*time.Second)
	if remainingPasswordTime <= 0 {
		return fmt.Errorf("no time left to wait for password prompt: %v", remainingPasswordTime)
	}

	if err := vm.Expect("Password:", remainingPasswordTime); err != nil {
		return fmt.Errorf("password prompt missing: %w", err)
	}
	if err := vm.Send(rootPassword); err != nil {
		return err
	}

	remainingPromptTime := budgetRemaining(startTime, c.maxRunTime, 5*time.Second)
	if remainingPromptTime <= 0 {
		return fmt.Errorf("no time left to wait for shell prompt: %v", remainingPromptTime)
	}

	if err := vm.Expect("#", remainingPromptTime); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	c.Println("Starting test suite ...")
	if err := vm.Send(`
mkdir -p /tmp/tests
mount /dev/disk/by-label/TESTDATA /tmp/tests
cd /tmp/tests
./main.sh
echo "TEST_RESULT:${?}"
`); err != nil {
		return err
	}

	waitLeft := budgetRemaining(startTime, c.maxRunTime, c.waitTests)
	if waitLeft <= 0 {
		return fmt.Errorf("invalid wait time for tests: %v", waitLeft)
	}

	c.Printf("Waiting for test suite to complete. Remaining budget: %v\n", waitLeft)
	if err := vm.Expect("TEST_RESULT:0", waitLeft); err != nil {
		return fmt.Errorf("Test suite failed: %w", err)
	}

	waitLeft = budgetRemaining(startTime, c.maxRunTime, c.waitTests)
	if waitLeft <= 0 {
		return fmt.Errorf("no time left to wait for VM shutdown: %v", waitLeft)
	}

	c.Printf("Test suite passed, shutting down VM. Remaining budget: %v\n", waitLeft)
	if err := vm.Send("poweroff"); err != nil {
		return err
	}

	waitLeft = budgetRemaining(startTime, c.maxRunTime, c.waitTests)
	if waitLeft <= 0 {
		return fmt.Errorf("no time left to wait for VM shutdown: %v", waitLeft)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), waitLeft)
	defer shutdownCancel()
	c.Printf("Waiting for VM to shutdown. Remaining budget: %v\n", waitLeft)

	done := make(chan error, 1)
	go func() {
		done <- vm.Wait()
	}()

	select {
	case err := <-done:
		// VM exited voluntarily, check if it was a clean shutdown
		if err != nil {
			return fmt.Errorf("VM shutdown failed: %w", err)
		}
		// fall through to success.
	case <-shutdownCtx.Done():
		c.Printf(
			"VM did not shutdown in time, killing process... Remaining budget: %v\n",
			waitLeft,
		)
		cancel()

		err = <-done // wait for the kill to complete.
		if err != nil {
			return fmt.Errorf(
				"VM shutdown failed, ctx err: %v: %w",
				ctx.Err(),
				err,
			)
		}
		return fmt.Errorf("VM shutdown timed out and was killed")
	}

	c.Printf("\n%s%sSUCCESS: Tests passed.%s\n", c.cBold, c.cGreen, c.cReset)
	return nil
}
