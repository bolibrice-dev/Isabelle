package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

var notification_mutex sync.Mutex
var notification_list map[string]int32
var sound_analysis_in_progress = false
var sound_count = 0
var last_sound_count = 0

type UserTokens struct {
	Id      int    `json:"id"`
	DToken  string `json:"dToken"`
	UToken  string `json:"uToken"`
	Dev     string `json:"dev"`
	UzType  string `json:"uztype"`
	UzPhone string `json:"uzphone"`
	Ts      int    `json:"ts"`
}

var last_report = time.Now().Add(-time.Duration(25) * time.Minute)

func monitorIfMoreSoundsAdded(ip string, utk []UserTokens) {
	for {
		notification_mutex.Lock()
		last_sound_count = sound_count
		notification_mutex.Unlock()
		time.Sleep(time.Second * 3)
		if last_sound_count == sound_count { //no change
			//fmt.Printf("\r\nSound analysis complete %v", notification_list)
			last_sound_count = 0
			sound_count = 0
			noti_sent := false

			for k := range notification_list {
				if notification_list[k] >= 5 && !noti_sent {
					noti_sent = true
					go notifyUsersFinal(ip, utk)
				}
				delete(notification_list, k)
			}

			break
		}

	}
	sound_analysis_in_progress = false
}
func shouldWeNotifyUsers(ip string, utk []UserTokens) {
	// we need to kinda wait here and see if there are multiple notifications
	// so let's get cam label and ID

	//fmt.Printf("\r\nInside shouldWeNotifyUsers( )")

	// add cam to sound array
	if !sound_analysis_in_progress {
		sound_analysis_in_progress = true
		// launch a background thread to
		// check if more sounds have been added
		go monitorIfMoreSoundsAdded(ip, utk)
	}
}
func notify_admin_about_low_disk_space() {
	rows := getAllAdminTokens()
	defer rows.Close()

	var utk []UserTokens
	rowsToStruct(rows, &utk)

	for _, rec := range utk {
		msg := `Your C3 box has ran out of space. 
		No more video recording until you delete some videos`
		//if rec.Dev == "ios" {
		evt := "Disk full notification"
		sendPush(rec.Dev, rec.DToken, evt, msg, "diskfull")
	}
}
func notifyPersonDetected(ip string) {
	cid := getCamIDFromIP(ip)
	lbl := getCamLabelFromIP(ip)

	rows := getAllDevTokens()
	defer rows.Close()

	var utk []UserTokens
	rowsToStruct(rows, &utk)

	for _, rec := range utk {
		msg := fmt.Sprintf("Camera %v (%v) reported seeing someone moving about",
			cid, lbl)
		//if rec.Dev == "ios" {
		evt := "Camera event"
		sendPush(rec.Dev, rec.DToken, evt, msg, "person")
	}
}
func notifyUsersFinal(ip string, utk []UserTokens) {
	cid := getCamIDFromIP(ip)
	lbl := getCamLabelFromIP(ip)
	for _, rec := range utk {
		msg := fmt.Sprintf("Camera %v (%v) reported unusually loud sound",
			cid, lbl)
		//if rec.Dev == "ios" {
		evt := fmt.Sprintf("Camera %v event", cid)
		sendPush(rec.Dev, rec.DToken, evt, msg, "loudsound")
	}
	last_report = time.Now()
}
func notifyUsersOfNoise(ip string) {
	notification_mutex.Lock()
	//print("\r\nNotifying registered users of noise")

	var tdiff = time.Since(last_report)
	minutes := tdiff.Minutes()
	if minutes >= 15 {

		if notification_list == nil {
			notification_list = make(map[string]int32)
		}
		if elm, exists := notification_list[ip]; exists {
			elm += 1
			notification_list[ip] = elm
		} else {
			notification_list[ip] = 1
		}
		sound_count++
		rows := getAllDevTokens()
		defer rows.Close()

		var utk []UserTokens
		rowsToStruct(rows, &utk)

		//print("\r\nElapsed: ", minutes)

		go shouldWeNotifyUsers(ip, utk)
	}
	notification_mutex.Unlock()
}

func sendPush(os_type string, dTok string, title string, msg string, _type string) {
	ajax := url.Values{}
	ajax.Add("cmd", "push_it")
	ajax.Add("tk", dTok)
	ajax.Add("tt", title)
	ajax.Add("mg", msg)
	ajax.Add("type", _type)

	url := "https://cashan.net/camview/mobapp/"
	if os_type == "ios" {
		url += "push.php"
	} else {
		url += "push_google.php"
	}
	resp, err := http.PostForm(url, ajax)
	resp.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	if err == nil {
		_, _ = io.ReadAll(resp.Body)
	}
}
func sendPushBtok(title string, msg string) {
	ajax := url.Values{}
	ajax.Add("cmd", "get_btok")
	url := "https://cashan.net/camview/mobapp/"
	resp, err := http.PostForm(url, ajax)
	resp.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	if err == nil {
		r, _ := io.ReadAll(resp.Body)
		var js interface{}
		if err = json.Unmarshal([]byte(r), &js); err == nil {
			data := js.(map[string]interface{})
			btok := data["btok"]
			btos := data["os"]
			sendPush(btos.(string), btok.(string), title, msg, "private")
			fmt.Printf("\r\npush btok result: \r\n[%v] / [%v]", btok, btos)
		}
	}
}

func sendMeText(txt string) {
	ajax := url.Values{}
	ajax.Add("cmd", "send_cheers")
	ajax.Add("msg", txt)

	url := "https://cashan.net/camview/mobapp/"
	resp, err := http.PostForm(url, ajax)
	resp.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	if err == nil {
		bt, _ := io.ReadAll(resp.Body)
		fmt.Printf("\r\nbt result: %v", string(bt))
	}
}
