package fronius

// Fronius controls the inverter battery. SunSpecType selects how the storage
// registers are located; the zero value keeps the legacy int+SF addresses.
type Fronius struct {
	SunSpecType SunSpecType
}
