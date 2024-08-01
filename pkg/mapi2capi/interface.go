package mapi2capi

type ProviderSpec interface {
	ToMachineTemplateSpec()
}

type Machine interface {
	ToMachineAndMachineTemplate()
}

type MachineSet interface {
	ToMachineSetAndMachineTemplate()
}
