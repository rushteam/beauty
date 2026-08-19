module github.com/rushteam/beauty/contrib/mqtt

go 1.26.0

toolchain go1.26.5

require (
	github.com/eclipse/paho.mqtt.golang v1.5.0
	github.com/rushteam/beauty v0.7.3
)

require (
	github.com/gorilla/websocket v1.5.3 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
)

replace github.com/rushteam/beauty => ../../
