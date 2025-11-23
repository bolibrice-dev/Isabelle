package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"time"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

var last_time_person_detected = time.Now()

type Detection struct {
	Class      string    `json:"class"`
	Confidence float64   `json:"Confidence"`
	Box        []float64 `json:"box"`
}
type Resp struct {
	Detections []Detection `json:"detections"`
}

func exec_AI_process() {
	cmd := exec.Command("python3", "detect.py")
	cmd.Stderr = io.Discard //os.Stderr- for debugging only
	cmd.Stdout = io.Discard // os.Stdout
	cmd.Start()
}

func reportPersonDetected(ip string) {
	cam_activity_time_mutex.Lock()
	var tdiff = time.Since(last_time_person_detected)
	minutes := tdiff.Minutes()

	last_cam_activity_time[ip] = time.Now()
	//fmt.Printf("\r\n---------Person detected on ip: %v", ip)

	if minutes >= 30 {
		notifyPersonDetected(ip)
		last_time_person_detected = time.Now()
	}
	cam_activity_time_mutex.Unlock()
}

func detect_for_python(url string, ip string) {
	fmt.Printf("\r\nSending frame from '%v' to python", url)
	log.SetOutput(io.Discard) //os.Stderr)
	rtsp := url
	for {
		jpg, err := grab_stream(rtsp)
		if err != nil {
			log.Println("grab frame error:", err)
			time.Sleep(3 * time.Second)
			continue
		}
		resp, err := http.Post("http://localhost:8000/detect",
			"application/octet-stream", bytes.NewReader(jpg))
		if err != nil {
			log.Printf("\r\ndetect post:", err)
			time.Sleep(5 * time.Second)
			continue
		}
		var r Resp
		_ = json.NewDecoder(resp.Body).Decode((&r))
		resp.Body.Close()

		for _, d := range r.Detections {
			if d.Class == "person" && d.Confidence > 0.6 {
				fmt.Printf("\r\nPerson detect at: %v from ip: %v",
					time.Now().Format("15:04:05"), ip)
				go reportPersonDetected(ip)
				//go recordAllCams(5)
			} /*else if d.Class == "car" && d.Confidence >= 0.6 {
				fmt.Printf("\r\n%v detected @ x=%v: %v; IP: %v",
					d.Class, d.Box[0], d.Confidence, ip)
			}*/
		}
	}
}

func grab_stream(rtspURL string) ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	err := ffmpeg.
		Input(rtspURL, ffmpeg.KwArgs{"rtsp_transport": "tcp"}).
		Output("pipe:", ffmpeg.KwArgs{
			"vframes": 1, "format": "image2", "vcodec": "mjpeg", "loglevel": "quiet",
		}).
		WithOutput(buf).
		WithErrorOutput(nil).
		Run()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
