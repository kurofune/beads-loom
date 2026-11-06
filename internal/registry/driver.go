package registry

import "database/sql/driver"

// driverBadConn is database/sql/driver.ErrBadConn, aliased so isConnErr can
// match it without importing the driver package at every call site.
var driverBadConn = driver.ErrBadConn
