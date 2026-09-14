package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	dir := flag.String("dir", `D:\download`, "要共享的文件夹")
	port := flag.String("port", "8080", "监听端口")
	key := flag.String("key", "", "访问密钥；设置后 URL 必须带 ?key=")
	flag.Parse()

	fileServer := http.FileServer(http.Dir(*dir))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if *key != "" && r.URL.Query().Get("key") != *key {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	if *key == "" {
		log.Println("提示: 未设置 key，任何能连到你机器的人都能访问；公网暴露时请加 -key")
	}
	log.Printf("fileShare 已启动: http://127.0.0.1:%s -> %s", *port, *dir)
	log.Fatal(http.ListenAndServe(":"+*port, mux))
}
