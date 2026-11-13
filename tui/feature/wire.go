package feature

import (
	"errors"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// WireMessage is what a failed call says to the user: the daemon's constant message, or the error.
func WireMessage(err error) string {
	var e *golibrpc.Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}

// Code is a failed call's error code, 0 when it is not the daemon's.
func Code(err error) int64 {
	var e *golibrpc.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}
