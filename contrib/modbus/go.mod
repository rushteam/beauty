module github.com/rushteam/beauty/contrib/modbus

go 1.26.0

toolchain go1.26.5

require (
	github.com/grid-x/modbus v0.0.0-20260701064235-82e41c9acfb6
	github.com/rushteam/beauty v0.7.3
)

require github.com/grid-x/serial v0.0.0-20211107191517-583c7356b3aa // indirect

replace github.com/rushteam/beauty => ../../
