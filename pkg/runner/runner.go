package runner

import (
	"fmt"
	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/request"
)

func RunSingleFile(file string, r *Opts) {
	req := request.ParseMonFile(file)
	res := net.HTTPRequest(req, r.HeaderOnly)
	if res != nil {
		fmt.Println(res)
	}
}
