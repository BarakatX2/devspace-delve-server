# devspace-delve-server

A tool for running a golang Delve server that automatically builds the package when a debug client connects.

```
go run main.go <go package/filename> <arguments>
```

Example:
```
go build -o delve-debug-server main.go
cp delve-debug-server <path to package>
delve-debug-server --listen=:2345 main.go 1 2 3
```

While the server is running, a Delve compatible debug client can connect to it. On connect, the package will be built and a Delve server will run. If the debug client terminates, the Delve instance will terminate. Currently only supports one connection at a time.