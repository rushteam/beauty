package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

func getData() []byte {
	var data = map[string]interface{}{
		"args": os.Args,
		"env":  os.Environ(),
	}
	bts, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		panic(err)
	}
	return bts
}

/*
telnet测试
GET / HTTP/1.1
Host: 192.168.58.142:8888
Connection: keep-alive
*/
func main() {
	// 此代码在telnet中是长连接的
	// 因为没有设置内容长度，传输会变成 Transfer-Encoding: chunked
	http.HandleFunc("/1", func(w http.ResponseWriter, r *http.Request) {
		w.Write(getData())
	})

	// 强行关闭长连接
	http.HandleFunc("/2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		w.Write(getData())
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		bts := getData()
		// 因为设置了content-length，所以不再是 Transfer-Encoding: chunked
		// 响应头content-length会自动转换为规范的大小写
		w.Header().Set("ContenT-LeNgth", fmt.Sprintf("%d", len(bts)))
		w.Write(bts)
	})

	log.Fatal(http.ListenAndServe(":8888", nil))
}
