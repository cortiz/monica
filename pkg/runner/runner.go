package runner

import (
	"fmt"

	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/request"
)

func RunSingleFile(file string, r *Opts) {
	req := request.ParseMonFile(file)
	res, err := net.HTTPRequest(req, r.HeaderOnly)
	if err != nil {
		return
	}
	if res != nil {
		fmt.Println(res)
	}
}
