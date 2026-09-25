package unisub

// Business record IDs do not depend on any proxy resource implementation.
func newID() string {
	id, err := randomID(16)
	if err != nil {
		panic(err)
	}
	return id
}
