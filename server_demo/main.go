/*
@Author: xiaobo
@Date: 2024/8/31 15:19
@Description:
*/

package main

import (
	"github.com/Xbzzy/client_demo/server_demo/common/simplelog"
	"github.com/Xbzzy/client_demo/server_demo/process"
	"net/http"
)

var GLogger simplelog.LogI

func main() {
	GLogger = simplelog.InitZapLog("debug", "./", "output", 1, 7)

	process.InitHttp(GLogger)

	err := http.ListenAndServe(":5999", nil)
	if err != nil {
		panic("http server start err:" + err.Error())
	}
}
