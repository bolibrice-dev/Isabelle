package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func enableWebServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleHome)
	mux.HandleFunc("/viewmp4", handleMP4)
	mux.HandleFunc("/numMp4", handleGetNumMP4s)
	mux.HandleFunc("/cmd_del", handleDelCmd)
	mux.HandleFunc("/numcams", handleGetNumCams)
	mux.HandleFunc("/getcams", handleGetMP4s)

	for {
		print("\r\n-------------------------launching web server")
		if err := http.ListenAndServe(":8089", mux); err != nil {
			log.Printf("web server exited: %v (retrying in 5s)", err)
			time.Sleep(5 * time.Second)
			continue
		}
		log.Println("web server stopped gracefully")
		return
	}
}

func handleDelCmd(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	idx := "-1"
	c := ""
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if _, exists := q["i"]; exists {
		idx = q["i"][0]
		c = q["c"][0]

		fpath := fmt.Sprintf("recordings/%v/%v", c, idx)

		_ = os.Remove(fpath)
		w.Write([]byte(fmt.Sprintf("path is %v;", fpath)))
		return
	}

	w.Write([]byte(fmt.Sprintf("idx is %v; ip is %v", idx, c)))
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	sendFile(w, r, "./home.htm")

}
func handleGetMP4s(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	//fmt.Printf("\r\nrequest: %v", q)
	cam := "0"
	start := "0"
	if _, exists := q["cam"]; exists {
		cam = q["cam"][0]
	}
	if _, exists := q["start"]; exists {
		start = q["start"][0]
	}
	ajax := getArchiveStartingAt(cam, start)
	//print(ajax)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(fmt.Sprintf("%v", ajax)))
}

func handleGetNumCams(w http.ResponseWriter, r *http.Request) {
	cnt := getCameraCount()
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(fmt.Sprintf("%v", cnt)))

}
func handleGetNumMP4s(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var cnt uint32 = 0
	if _, exists := q["cam"]; exists {
		cam := q["cam"][0]
		cid, err := strconv.Atoi(cam)
		if err == nil {
			cnt, _ = getVideoCount(cid)
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Write([]byte(fmt.Sprintf("%v", cnt)))
		}

	}
}
func handleMP4(w http.ResponseWriter, r *http.Request) {
	//fmt.Printf("\r\n+++++file name: %v", br.fn)
	q := r.URL.Query()
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if _, exists := q["mp4"]; exists {
		mp4 := q["mp4"][0]
		sendFile(w, r, "./recordings/"+mp4)
	} else {
		http.Error(w, "the fuck?", http.StatusBadRequest)
	}
	//fmt.Println("GET params were:", q["mp4"][0])
}

func sendFile(w http.ResponseWriter, r *http.Request, filePath string) {

	// Check if the file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		fmt.Printf("\r\nFile nopt found %v", filePath)
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	if strings.Contains(filePath, ".mp4") {
		w.Header().Set("Content-Type", "video/mp4")
	} else {
		w.Header().Set("Content-Type", "text/html")
	}

	// Set the Content-Type header (optional, http.ServeFile often infers it)
	//w.Header().Set("Content-Type", "application/octet-stream")
	// Or a more specific type like "text/plain" for a text file

	// Serve the file
	http.ServeFile(w, r, filePath)
}
