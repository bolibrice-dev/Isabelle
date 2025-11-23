package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
	"github.com/use-go/onvif"
	"github.com/use-go/onvif/device"
	"github.com/use-go/onvif/xsd"
	onvifxsd "github.com/use-go/onvif/xsd/onvif"
	"github.com/videonext/onvif/discovery"
)

// for now we only support reporting lound sound
// object detection will come next
// pip install deep_sort_realtime --break-system-packages

// sudo apt install libopencv-dev
// pip install --upgrade torch --break-system-packages
// pip install --upgrade yolov5  --break-system-packages # or ultralytics

var cherry_conn *websocket.Conn
var AI_Conn *websocket.Conn
var mytoken string
var cam_is_recording_guard sync.Mutex
var pcMap map[string](*webrtc.PeerConnection)

var this_version = "2025.8.1"

type CamCam struct {
	Id           int    `json:"id"`
	Ip           string `json:"ip"`
	Label        string `json:"label"`
	Sdate        string `json:"sdate"`
	Avail        int    `json:"avail"`
	Allow_Map    int    `json:"allow_map"`
	Allow_pr     int    `json:"allow_pr"`
	Show_count   int    `json:"show_count"`
	ALlow_report int    `json:"allow_report"`
	LatLon       string `json:"ltln"`
}

func UNUSED_UINT32(p uint32) {
	_ = p
}
func UNUSED_MOXER(elm *Moxer) {
	_ = elm
}

/*
	camera_events = {
		"evt_recording":{
			"camera": {"1","2"}//camera id/index
		}
	}
*/
var (
	cam_is_recording   = make(map[string]bool)
	cmd_exec_ptr       = make(map[string]*exec.Cmd)
	cam_exec_ptr_guard sync.Mutex
	//most_recent_recording = time.Now()
)
var last_cam_activity_time = make(map[string]time.Time)
var cam_activity_time_mutex sync.Mutex
var disk_msg_sent = false

func checkForSWUpgrades() {
	now_time := time.Now()
	for {
		if now_time.Minute() == 15 && now_time.Local().Hour() == 22 {
			log.Println("Time to check for SW updates")
			get_new_SW()

			time.Sleep(5 * time.Minute)

		}
		now_time = time.Now()
		time.Sleep(5 * time.Second)
		//fmt.Printf("\r\ncurrent time hh/mm: %v/%v",
		//	now_time.Local().Hour(), now_time.Minute())

	}
}

func get_new_SW() {
	ajax := url.Values{}
	ajax.Add("cmd", "get_sw_version")

	url := "https://cashan.net/camview/mobapp/sw-update/"
	resp, err := http.PostForm(url, ajax)
	resp.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	if err == nil {
		r, _ := io.ReadAll(resp.Body)
		var js interface{}
		if err = json.Unmarshal([]byte(r), &js); err == nil {
			data := js.(map[string]interface{})
			f := data["f"].(string)
			f2 := strings.Split(f, ";")
			v := data["v"].(string)
			fmt.Printf("r\nversion: %v\r\nfiles: ", v)
			// for _, y := range f2 {
			// 	print(" --", y)
			// }
			if v != this_version {
				fmt.Printf("\r\nOld version is %v; new is %v; upgrading", this_version, v)
				for _, y := range f2 {
					print(" --", y)
					download_file("https://cashan.net/camview/mobapp/sw-update/", y)
					if y == "camview" {
						os.Chmod("./"+y, 0755)
					}
				}
				sendMeText(fmt.Sprintf("Update for %v", mytoken))
				cmd := exec.Command("systemctl", "reboot")
				_ = cmd.Run()
			} else {
				fmt.Printf("\r\nSoftware at latest version. No update needed...")
			}

		}
	}
}

func currentTime() string {
	now := time.Now()
	return now.Format("2006-01-02 15:04:05")
}

func clearScreen() {
	fmt.Print("\033[H\033[2J") // Clear screen escape sequence
}

func download_file(url string, filepath string) {
	resp, err := http.Get(url + filepath)
	print("\r\ndownloading.........", url, filepath)
	if err == nil {
		defer resp.Body.Close()

		//create file

		// copy response body to file
		fmt.Printf("\r\nresp.status: %v", resp.Status)
		if strings.Contains(resp.Status, "200 OK") {

			//var buf []byte
			n, _ := io.ReadAll(resp.Body)
			if n[0] != '<' && n[1] != '!' { //!(strings.Contains(string(n), "<html>")) {
				temp := "./" + filepath + ".new"
				out, err := os.Create(temp)
				if err == nil {
					defer out.Close()
					print("\r\ncopying downloaded content to: ", temp)
					buf := bytes.NewBuffer(n)
					_, err = io.Copy(out, buf)

					if err != nil {
						print("\r\n-----------i dunno wtf happened with the content")
					} else {
						f_old := "./" + filepath
						f_new := temp
						os.Rename(f_new, f_old)
					}

				}
			} else {
				print("\r\n---------hmm did not get the right file", string(n))
			}
			//fmt.Printf("\r\nbuffer read (%v): %v", n, string(n))

		}
	}

}

func saveRecording(cam string, filename string, duration int) {
	// move to appropriate folder
	folder := "./recordings/" + cam + "/"
	err := os.MkdirAll(folder, 0755)
	org_file := "./recordings/" + filename + ".ts"
	if err == nil {
		//fmt.Printf("\r\nstring '%v' trying to remove %v", filename, cam)

		filename += ".mp4"
		//fmt.Printf("\r\nstring is now %v", filename)

		////
		newPath := folder + filename
		newPath = strings.ReplaceAll(newPath, cam, "")
		newPath = strings.ReplaceAll(newPath, "//", "/")
		newPath = strings.ReplaceAll(newPath, "/recordings/", "/recordings/"+cam+"/")

		// for now just delete and exit (debug only)

		cam_activity_time_mutex.Lock()
		if _, exists := last_cam_activity_time[cam]; exists {
			last_reported := time.Since(last_cam_activity_time[cam])
			if int(last_reported.Minutes()) <= duration {
				//save
				//fmt.Printf("\r\nNow converting .ts to .mp4...last reported=> %v minutes ago", last_reported.Minutes())
				cmd_convert := exec.Command("ffmpeg",
					"-i", org_file,
					"-c", "copy",
					"-movflags", "+faststart", newPath)
				cmd_convert.Run()
				os.Remove(org_file)
				//cmd := cmd_exec_ptr[cam]
				//_ = os.Rename("./recordings/"+filename, newPath)
			} else {
				os.Remove(org_file)
			}
		} else {
			// 	//fmt.Printf("\r\nit does not exist") // just delete
			os.Remove(org_file)
		}
		// os.Remove(org_file)
		cam_activity_time_mutex.Unlock()
	}
}

//fmt.Printf("\r\nEnded saving mp4 for camera %v", cam)
//os.Remove(org_file)

// this function is called 'once' at bootup
func recordAllCams(duration int) {
	time.Sleep(7 * time.Second)
	cams := getCameras()
	var cmcm []CamCam
	rowsToStruct(cams, &cmcm)

	for _, acam := range cmcm {
		ip := acam.Ip
		go startCameraRecording(ip, "any", duration)
	}
}
func startCameraRecording(cam string, what string, duration int) {
	folder := "./recordings"

	//fmt.Printf("\r\nStarting recording for '%v' (%v min) ", cam, duration)

	// 59,664,031,744
	ds := getAvailableDiskSpace()
	if ds <= 15000000 {
		print("\r\n")
		log.Printf("-------Not enough space to record video...only have %v", ds)
		if !disk_msg_sent {
			go notify_admin_about_low_disk_space()
			disk_msg_sent = true
			_ = disk_msg_sent
		}
		return
	}
	//log.Printf("xxxxx We have enough space to record video...have %v", ds)
	//most_recent_recording = time.Now() // refresh to start over
	//fmt.Printf("\r\n processing request to record camera %v", cam)

	err := os.MkdirAll(folder, 0755)
	if err != nil {
		print("\r\n")
		log.Println("Serious error. Couldn't create recordings folder", err)
		return
	}
	//cam_is_recording[cam] = true
	//cam_is_recording_guard.Unlock()
	now := time.Now()

	//sz, camlist := Config.list()
	filename := what + cam + "_" + now.Format("2006-01-02 15:04:05")

	RTSP_URL := "rtsp://admin:123456@" + cam + ":554/Streaming/channels/101"

	cam_exec_ptr_guard.Lock()

	cmd_exec_ptr[cam] = exec.Command("ffmpeg",
		"-rtsp_transport", "tcp",
		"-i", RTSP_URL,
		"-t", fmt.Sprintf("%v", duration*60),
		"-map", "0",
		"-f", "mpegts",
		"-c:v", "copy",
		"-c:a", "aac",
		folder+"/"+filename+".ts")

	cid := getCamIDFromIP(cam)
	activateEventForAllUsers("vdorec", cid)
	cmd := cmd_exec_ptr[cam]

	cam_exec_ptr_guard.Unlock()
	//log.Printf("\r\n now recording %v on camera %v", what, cam)
	//locked = false
	//cam_is_recording_guard.Unlock()
	cmd.Run()

	// next command to run to convert from .ts to .mp4
	// ffmpeg -i ./output.ts -c copy -movflags +faststart ./output.mp4
	go saveRecording(cam, filename, duration)
	go startCameraRecording(cam, what, duration)
}
func genUUID() string {
	id := uuid.New()
	return id.String()
}
func getAvailableDiskSpace() uint64 {
	var stat syscall.Statfs_t
	path := "./recordings"
	err := syscall.Statfs(path, &stat)
	if err == nil {
		available := stat.Bavail * uint64(stat.Bsize)
		//fmt.Printf("\r\nAvailable space on disk: %v", available)
		return available
	}
	return 0
}

func PlaceCamsInStruct() {
	camcount := getCameraCount()
	//mp4stream.Load("recordings/192.168.0.120/any_2025-10-29 20:45:34.mp4")
	index := 0
	if camcount > 0 {
		fmt.Printf("\r\nSystem has %d camera(s)\n", camcount)

		// remove existing elements from map
		// TODO: we need to update database
		// for when a cam is offline or removed
		for j, _ := range Config.Streams {
			delete(Config.Streams, j)
		}

		//the_url := ""

		cams := getCameras()
		var cmcm []CamCam
		rowsToStruct(cams, &cmcm)
		//print("\r\nLen result ", len(cmcm))

		for _, acam := range cmcm {
			//fmt.Printf("\r\nip:%v, sdate: %v ", acam.Ip, acam.Sdate)

			url := fmt.Sprintf("rtsp://admin:123456@%s:554/Streaming/channels/101", acam.Ip)
			go detect_for_python(url, acam.Ip)
			cam_is_recording_guard.Lock()
			cam_is_recording[acam.Ip] = false
			cam_is_recording_guard.Unlock()
			// make sure camera time is current
			onvifAddress := fmt.Sprintf("%v", acam.Ip)
			onvifUser := "admin"
			onvifPass := "123456"

			dev, err := onvif.NewDevice(onvif.DeviceParams{
				Xaddr:    onvifAddress,
				Username: onvifUser,
				Password: onvifPass,
			})

			if err == nil {
				//print("\r\ndev: ", dev)
				now := time.Now().UTC()
				//location := now.Location()
				//zone, _ := now.Zone()
				tz := time.Now().Local()
				tzn, _ := tz.Zone()
				fmt.Printf("\r\ncurrent hour + zone: %v / %v",
					now.Hour(), tzn)
				req := device.SetSystemDateAndTime{
					DateTimeType:    "Manual",
					DaylightSavings: true,
					TimeZone:        onvifxsd.TimeZone{TZ: xsd.Token(tzn)},
					UTCDateTime: onvifxsd.DateTime{
						Time: onvifxsd.Time{
							Hour:   xsd.Int(now.Hour()),
							Minute: xsd.Int(now.Minute()),
							Second: xsd.Int(now.Second()),
						},
						Date: onvifxsd.Date{
							Year:  xsd.Int(now.Year()),
							Month: xsd.Int(now.Month()),
							Day:   xsd.Int(now.Day()),
						},
					},
				}
				_, _ = dev.CallMethod(req)
				//fmt.Printf("Response: %+v", resp)
			} else {
				print("\r\nerr: ", err.Error())
			}

			strm := StreamST{
				OnDemand:     true,
				DisableAudio: false,
				URL:          url,
			}
			Config.Streams[fmt.Sprintf("%d", index)] = strm
			index++
			initCamDetectionDB(acam.Ip)
		}

		for i, v := range Config.Streams {
			v.Cl = make(map[string]viewer)
			Config.Streams[i] = v
		}
	}
}

// comcast business: 8773201803930470 / Tonya2005 / cusadmin
// 50.187.168.14
func main() {
	clearScreen()

	folder := "./recordings"
	ext := ".ts"
	files, err := os.ReadDir(folder)
	//print("\r\nclearing up old .mp4s...%v total files in folder", files)
	if err == nil {
		for _, file := range files {
			//print("\r\nfile: %v", file.Name())
			if !file.IsDir() && strings.EqualFold(filepath.Ext(file.Name()), ext) {
				fullpath := filepath.Join(folder, file.Name())
				//fmt.Printf("\r\nDeleting %v", fullpath)
				_ = os.Remove(fullpath)
			}
		}
		//fpath := filepath.Join(folder, ext)
		//os.Remove(fpath)
	} else {
		print("\r\nNo recordings exist yet")
	}

	last_cam_activity_time["dummy"] = time.Now()

	camview_db := initDatabase("./camview.db")
	if camview_db != nil {
		defer camview_db.Close()
	}

	mytoken = getUUID()
	exec_AI_process()
	go keepCheckingIfCherryIsUp()

	go checkForSWUpgrades()
	pcMap = make(map[string]*webrtc.PeerConnection)

	// now that db is up, let's detect cameras on the connected network
	devices, err := discovery.StartDiscovery(2 * time.Second)

	if err == nil && len(devices) > 0 {
		fmt.Printf("\r\nWe discovered %v devices", len(devices))
		for _, device := range devices {
			//fmt.Printf("\r\ndevice: %v", device)
			parts := strings.Split(device.XAddr, "/")
			rows, _ := getCameraRecord(parts[2])
			if rows != nil {
				var ip_, date_ string
				rows.Next()
				rows.Scan(&ip_, &date_)
				if ip_ == "" {
					// new camera detected. Save to db
					addCameraRecord(parts[2])
				} else {
					// camera exists
					//fmt.Printf("\r\nCamera %v exists", parts[2])
				}

			}
			rows.Close()
		}
	}

	Config = loadConfig()
	PlaceCamsInStruct()

	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	ds := getAvailableDiskSpace()
	fmt.Printf("\r\navailable disk space: %v", ds)

	go recordAllCams(1)
	//go enableWebServer()
	/*StartMP4TestServer(":8085", "/home/beau/Documents/Isabelle/recordings/"+
	"192.168.0.120/any_2025-11-06 16:18:37.mp4")*/

	go func() {
		sig := <-sigs
		log.Println(sig)
		done <- true
	}()
	// log.Println("server started -  running until closed")
	<-done
	log.Println("closing cashan server")
}

func remote_change_cam_settings(m map[string]interface{}) {
	//from := m["mine"]
	cam := m["cam"].(string)
	key := m["settings"].(string)
	val := m["active"].(string)

	//print("\r\nremote settings request from", from)
	//fmt.Printf("\r\ncam:%v; key: %v; val: %v", cam, key, val)
	changeCamSettings(cam, key, val)
}

// calls from remote
func remote_getCamList(from string) {
	cams := getCameras()
	mytok := getUUID()
	ajax := fmt.Sprintf(`{"msg":"relay_msg","mine":"%s","hers":"%s","request":"cam_list","cameras":[`, mytok, from)

	if cams != nil {
		var cmcm []CamCam
		rowsToStruct(cams, &cmcm)
		for _, c := range cmcm {
			record := fmt.Sprintf(`{"id":"%d","ip":"%s","label":"%s",
					"date":"%s","s0":%d,"s1":%d,"s2":%d,"s3":%d,"s4":"%s","s5":%d},`,
				c.Id, c.Ip, c.Label, c.Sdate, c.Avail, c.Allow_pr,
				c.Show_count, c.ALlow_report, c.LatLon, c.Allow_Map)
			ajax += record
		}
		ajax += "]"
		ajax = strings.Replace(ajax, ",]", "]}", 1)
		cherry_conn.WriteMessage(websocket.TextMessage, []byte(ajax))

		ltln := getCS_latlng()
		ajax = fmt.Sprintf(`{"msg":"relay_msg","mine":"%s","hers":"%s","request":"cs_ltln","ltln":"%s"}`,
			mytok, from, ltln)
		cherry_conn.WriteMessage(websocket.TextMessage, []byte(ajax))

		//print("\r\ncamera latlng is: ", ltln)
	}
}

/*
pip install torch torchvision torchaudio
pip install opencv-python
git clone https://github.com/ultralytics/yolov5
cd yolov5
pip install -r requirements.txt
956d8cc5-6ce4-46a1-9a0c-2da6a447d8c9
*/
