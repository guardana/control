//go:build !unix

package gatewayconfig

import "errors"

// accessWrite cannot ask this platform whether the plane may write a path,
// so no path is shown closed to it.
func accessWrite(string) (bool, error) {
	return false, errors.New("this platform cannot say whether the plane's account may write a path")
}
