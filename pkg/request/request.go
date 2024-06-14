package request

type Request struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    string
}

func (r Request) String() string {
	return r.Method + " " + r.URL
}
