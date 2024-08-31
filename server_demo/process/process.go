/*
@Author: xiaobo
@Date: 2024/8/31 15:23
@Description:
*/

package process

import (
	"github.com/Xbzzy/client_demo/server_demo/common/simplelog"
	"github.com/Xbzzy/client_demo/server_demo/common/util"
	"go.uber.org/zap"
	"net/http"
	"time"
)

func SafeHttpRegister(l simplelog.LogI, pattern string, handler func(simplelog.LogI, http.ResponseWriter, *http.Request)) {
	http.HandleFunc(pattern, func(writer http.ResponseWriter, request *http.Request) {
		defer util.CaptureException()

		logger := l.Clone()
		logger.SetLogId(time.Now().UnixNano())

		logger.DebugWF("start http", zap.String("pattern", pattern), zap.Any("header", request.Header),
			zap.Any("host", request.Host), zap.Any("remoteAddr", request.RemoteAddr))

		handler(logger, writer, request)
	})
}

func InitHttp(l simplelog.LogI) {
	SafeHttpRegister(l, "/test1", func(logger simplelog.LogI, w http.ResponseWriter, q *http.Request) {

	})
}
