package common

import "fmt"

var ErrUnsupported = fmt.Errorf("unsupported operation")
var ErrCorrupt    = fmt.Errorf("corrupt or invalid data")
var ErrNotFound   = fmt.Errorf("not found")
