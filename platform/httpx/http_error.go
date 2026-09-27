package httpx

// Error represents a transport HTTP error with a status code and message.
type Error struct {
	Status int
	Msg    string
}

func (e Error) Error() string { return e.Msg }
