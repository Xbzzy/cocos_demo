/*
@Author: xiaobo
@Date: 2024/8/31 15:56
@Description:
*/

package util

import (
	"fmt"
	"runtime/debug"
)

func CaptureException() {
	if err := recover(); err != nil {
		stack := string(debug.Stack())
		fmt.Println("panic: Recovered in err", err, stack)
		return
	}
	return
}
