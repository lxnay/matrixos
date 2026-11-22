package commands

// DevCommand is an uber command for orchestrating the development toolkit and its workflow.
type DevCommand struct {
	groupCommand
}

// NewDevCommand creates a new DevCommand
func NewDevCommand() *DevCommand {
	return &DevCommand{
		groupCommand: newGroupCommand("dev", map[string]func() ICommand{
			"check":   func() ICommand { return NewCheckCommand() },
			"enter":   func() ICommand { return NewEnterCommand() },
			"janitor": func() ICommand { return NewJanitorCommand() },
			"vm":      func() ICommand { return NewVMCommand() },
		}),
	}
}
