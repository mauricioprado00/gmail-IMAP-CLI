package broker

// LeaseRequest identifies the authenticated IMAP connection a CLI process
// wants to lease from the local daemon.
type LeaseRequest struct {
	Host     string
	Port     int
	User     string
	Password string
	Debug    bool
}

type Request struct {
	Command    string
	Literal    []byte
	HasLiteral bool
}

type Line struct {
	Text     string
	Literals [][]byte
}

type Response struct {
	Untagged []Line
	Status   string
	Text     string
}

type Reply struct {
	Response *Response
	Error    string
}
