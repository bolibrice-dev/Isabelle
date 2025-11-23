package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

// need to install libopus like so:
// sudo apt update
// sudo apt install libopus-dev
// sudo apt install libopusfile-dev
type Peer struct {
	peer *webrtc.PeerConnection
}

type DataChannelMsg struct {
	Msg   string `json:"msg"`
	Param string `json:"param"`
}
type AccessCode struct {
	Net string `json:"net"`
	Tok string `json:"tok"`
}

var dcMessage DataChannelMsg
var cmutex sync.Mutex

var (
	pc_users      = make(map[string]uint32)
	soundMap      = make(map[string][]float64)
	PeerConn      = make(map[string]*Peer)
	peerMutex     sync.Mutex
	pc_users_sync sync.Mutex
	addr          = flag.String("adr", "cashan.net:8082", "WebSocket server address")
	path          = flag.String("pat", "/", "WebSocket request path")

	ai_addr = flag.String("addr", "127.0.0.1:8765", "WebSocket server address")
	ai_path = flag.String("path", "/", "WebSocket request path")
)

func add_pc_user(idx string, cnt uint32) {
	pc_users_sync.Lock()
	if elm, exists := pc_users[idx]; !exists {
		pc_users[idx] = 0
		UNUSED_UINT32(elm)
		//fmt.Printf("\r\nuser elm: %v", elm)
	}
	if cnt == 999 {
		if pc_users[idx] > 0 {
			pc_users[idx] -= 1
		}
	} else {
		pc_users[idx] += cnt
	}
	pc_users_sync.Unlock()
}

func keepCheckingIfCherryIsUp() {
	conn, err := connectCherry()
	//tok := uuid.New().String()
	if err == nil {
		print("\r\nconnected to cherry")
		go pollWebSocket(conn)
		cherry_conn = conn
		ajax := "{ \"msg\":\"new_connect\",\"tok\":\"" + mytoken + "\",\"uid\":\"" + mytoken + "\"}"
		conn.WriteMessage(websocket.TextMessage, []byte(ajax))
	} else {
		// hmm cherry not available? keep trying
		time.Sleep(5000 * time.Millisecond)
		go keepCheckingIfCherryIsUp()
	}
}

func connectAI() (*websocket.Conn, error) {
	flag.Parse()
	u := url.URL{Scheme: "ws", Host: *ai_addr, Path: *ai_path}
	header := http.Header{}
	header.Set("Sec-WebSocket-Protocol", "Isabelle")
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		return nil, err
	}
	return conn, err
}

func connectCherry() (*websocket.Conn, error) {
	flag.Parse()

	// build the url
	u := url.URL{Scheme: "wss", Host: *addr, Path: *path}
	//log.Printf("Connecting to %s", u.String())
	header := http.Header{}
	header.Set("Sec-WebSocket-Protocol", "Olibrice")
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		return nil, err
	}
	return conn, err
}
func calculateSoundAverage(nbr []float64) float64 {
	if len(nbr) == 0 {
		return 0.0
	}
	sum := 0.0
	for _, num := range nbr {
		sum += num
	}
	return sum / float64(len(nbr))
}

func pollWebSocket(c *websocket.Conn) {
	defer c.Close()
	for {
		_, msg, err := c.ReadMessage()

		if err == nil {
			var data map[string]interface{}
			//var in_data map[string]interface{}
			err := json.Unmarshal([]byte(msg), &data)
			if err == nil {
				msg := data["msg"].(string)
				switch msg {
				case "relay_msg":
					processRelayedRequest(data)
				}
			}
			//log.Printf("\r\n--------------------------\r\nReceived: %s\r\n----------------------------", string(msg))
		} else { // cherry terminated??
			go keepCheckingIfCherryIsUp()
			return
		}
	}

}

func sendIceCandidate(mine string, hers string, sid string, candi string) {
	ajax := fmt.Sprintf("{\"msg\":\"relay_msg\",\"mine\":\"%s\",\"hers\":\"%s\","+
		"\"request\":\"cs_candi\",\"sid\":\"%s\",\"candi\":\"%s\"}", mine, hers, sid, candi)
	cherry_conn.WriteMessage(websocket.TextMessage, []byte(ajax))
}

func sendSDP(mine string, hers string, sid string, sdp string) {
	ajax := fmt.Sprintf("{\"msg\":\"relay_msg\",\"mine\":\"%s\",\"hers\":\"%s\","+
		"\"request\":\"my_sdp\",\"sid\":\"%s\",\"sdp\":\"%s\"}", mine, hers, sid, sdp)
	cherry_conn.WriteMessage(websocket.TextMessage, []byte(ajax))
}

func webrtcInnerLoop(AudioOnly bool, suuid string, muxerWebRTC *Moxer) {
	is_running := false
	current_cam := suuid
	//log.Println("current cam is ", current_cam)
	cid, ch := Config.clAd(current_cam)
	muxerWebRTC.SetCurrentCam(current_cam)
	defer Config.clDe(current_cam, cid)
	defer muxerWebRTC.Close()
	var videoStart bool
	noVideo := time.NewTimer(10 * time.Second)
	for {
		select {
		case <-noVideo.C:
			log.Println("noVideo on cam", current_cam, "reconnecting with cam")
			Config.RunIFNotRun(current_cam)
			//return
		case pck := <-ch:
			if pck.IsKeyFrame || AudioOnly {
				noVideo.Reset(10 * time.Second)
				videoStart = true
			}
			if !videoStart && !AudioOnly {
				//log.Println("video has not yet started")
				Config.RunIFNotRun(current_cam)
				continue
			}
			if muxerWebRTC.GetCurrentCam() == current_cam {
				var err = muxerWebRTC.WritePacket(pck)

				if err != nil {
					//log.Println("Unexpected Event for:", suuid, err)
					return
				}
				if !is_running {
					is_running = true
					//log.Println("Start WritePacket", current_cam)
				}
				//}
			} else {
				current_cam = muxerWebRTC.GetCurrentCam()

				Config.RunIFNotRun(current_cam)
				cid, ch = Config.clAd(current_cam)
				//log.Println("switching from ", old_cam, "to", current_cam)
				defer Config.clDe(current_cam, cid)
				noVideo = time.NewTimer(10 * time.Second)

				// let remote know cam has switched
			}
		}
	}
}
func myWebRTC_Datachannel(caller string, d *webrtc.DataChannel, cam_now string, element *Moxer) {

	isClosed := false
	num_dc := 0
	last_active_value := false
	d.OnOpen(func() {
		num_dc += 1
		add_pc_user(cam_now, 0)
		last_num_user := pc_users[cam_now]

		// store in c3_history
		phone := getPhoneFromToken(caller)
		if phone != "" {
			entry := fmt.Sprintf("User '%v' has joined camera %v", phone, cam_now)
			add_c3_history(entry)
		}

		go func() {
			for {
				// here we will listen to see if message need to be sent
				// messages related to events
				if isClosed {
					return
				}
				cid := getCamIDFromIP(cam_now)

				b := isEventActive("vdorec", caller, cid)
				if b != last_active_value {
					last_active_value = b
					ajax := ""
					if b {
						ajax = `{"msg":"vdorec_on"}`
					} else {
						ajax = `{"msg":"vdorec_off"}`
					}
					if d.ReadyState() == webrtc.DataChannelStateOpen {
						d.SendText(ajax)
						fmt.Printf("\r\n%v", ajax)
					}
				}

				time.Sleep(20 * time.Second)
			}

		}()
		go func() {
			pc_users_sync.Lock()
			if last_num_user != pc_users[cam_now] && pc_users[cam_now] > 0 {

				send_num_user_update(d, cam_now)
				last_num_user = pc_users[cam_now]
			}
			pc_users_sync.Unlock()
			time.Sleep(2 * time.Second)
		}()
	})
	d.OnClose(func() {
		phone := getPhoneFromToken(caller)
		if phone != "" {
			entry := fmt.Sprintf("User '%v' left camera %v", phone, cam_now)
			add_c3_history(entry)
		}
		StopInlineMP4Playback(element)

		add_pc_user(cam_now, 999) // 999 subtract 1
		//fmt.Printf("\r\ndata conn with '%v' closed; cam %v now has %v user(s)",
		//	caller, cam_now, pc_users[cam_now])
		evt := "vdorec"
		isClosed = true
		//fmt.Printf("\r\nremoving evt %v for camera %v ; caller %v", evt, cam_now, caller)
		deleteUserEventForCamera(caller, evt, cam_now)
		evt = "num_user"
		deleteUserEventForCamera(caller, evt, cam_now)
	})
	d.OnMessage(func(msg webrtc.DataChannelMessage) {
		err := json.Unmarshal(msg.Data, &dcMessage)
		if err == nil {
			switch dcMessage.Msg {
			case "mp4_client_ice":
				mp4_ice := dcMessage.Param
				//fmt.Printf("\r\nget client ice: %v", mp4_ice)
				go get_client_ice_mp4(mp4_ice)
			case "load_mp4":
				//fmt.Printf("\r\nload mp4--%v", cam_now)
				i_idx, err := strconv.Atoi(cam_now) //cam id offset by 1
				if err != nil {
					fmt.Printf("\r\nnot sure whats wrong with cam_now...")
					return
				}
				newid := i_idx + 1
				absPath, err := StartInlineMP4Playback(fmt.Sprint(newid), dcMessage.Param, element)
				if err != nil {
					fmt.Printf("inline mp4 load error: %v", err)
					sendInlineJSON(d, map[string]string{
						"msg":   "mp4_inline_error",
						"error": err.Error(),
					})
					break
				}
				sendInlineJSON(d, map[string]string{
					"msg":  "mp4_ready",
					"path": relativeRecordingPath(absPath),
				})

			case "swap_mp4":
				if absPath, err := StartInlineMP4Playback(cam_now, dcMessage.Param, element); err == nil {
					sendInlineJSON(d, map[string]string{
						"msg":  "mp4_inline_ready",
						"path": relativeRecordingPath(absPath),
					})
					break
				} else if err != nil {
					log.Printf("inline mp4 swap error: %v", err)
				}
				str := strings.Split(dcMessage.Param, "////")
				fmt.Printf("\r\nrequest to swap mp4 to %v", dcMessage.Param)

				i_idx, err := strconv.Atoi(str[0]) //cam id offset by 1
				if err != nil {
					fmt.Printf("\r\nnot sure whats wrong...")
					return
				}
				newid := i_idx + 1
				ip, _ := getCameraIPById(fmt.Sprint(newid))
				pth := fmt.Sprintf("%v/%v", ip, str[1])
				//relpath := mp4SelectRequest{Path: string(pth)}
				absPath, err := resolveRecordingPath(pth)
				if err != nil {
					fmt.Printf("\r\nnot sure still...")
					return
				}
				fmt.Printf("\r\nswitching to %v", absPath)
				err = SwapPeerConnectionPlayback(fmt.Sprint(newid), absPath)
				if err != nil {
					fmt.Printf("\r\nswap error: %v", err)
				}

			case "stop_mp4":
				StopInlineMP4Playback(element)
				sendInlineJSON(d, map[string]string{
					"msg": "mp4_stopped",
				})

			case "get_mp4":
				str := strings.Split(dcMessage.Param, "////")
				i_idx, err := strconv.Atoi(str[1]) //cam id offset by 1
				if err == nil {
					fmt.Printf("\r\ngetting mp4 for cam %v", i_idx)
				}
				rmsdp := str[2] //remote sdp

				remote_sdp, _ := base64.StdEncoding.DecodeString(rmsdp)

				offer := mp4OfferRequest{SDP: string(remote_sdp)}

				newid := i_idx + 1
				ip, _ := getCameraIPById(fmt.Sprint(newid))
				mp4_path := fmt.Sprintf("%v/%v", ip, str[3])
				abs, err := resolveRecordingPath(mp4_path)
				if err == nil {
					SwitchMP4PlaybackFile(abs)
				} else {
					fmt.Printf("\r\nserious error: %v", err)
					return
				}
				resp := processOffer(offer)
				if resp.Error != "" {
					fmt.Printf("\r\nerror----cheap")
				}
				fmt.Printf("\r\nstoring %v", newid)
				key := fmt.Sprintf("pc:%v", newid)
				storeMP4Playback(key, resp.Pback)

				pyl, err := json.Marshal(resp)
				if err != nil {
					log.Printf("mp4 answer marshal: %v", err)
					return
				}
				payload := string(pyl)

				sdp := strings.Replace(payload, "\r\n", "////", -1)
				ajax := fmt.Sprintf("{\"msg\":\"mp4_sdp\",\"param\":%v}", sdp)

				////fmt.Printf("\r\n\r\npayload: %v", sdp)

				if d.ReadyState() == webrtc.DataChannelStateOpen {
					d.SendText(ajax)
					//fmt.Printf("\r\n%v", ajax)
				}

			case "begin_view":
				add_pc_user(cam_now, 1)
				send_num_user_update(d, cam_now)
				phone := getPhoneFromToken(caller)
				if phone != "" {
					entry := fmt.Sprintf("User '%v' switched to camera %v", phone, cam_now)
					add_c3_history(entry)
				}
			case "end_view":
				if elm, exists := pc_users[cam_now]; exists {
					UNUSED_UINT32(elm)
					//pc_users[cam_now] -= 1
					add_pc_user(cam_now, 999) // subtract 1
					send_num_user_update(d, cam_now)
					//fmt.Printf("\r\nend view; cam %v now has %v user(s)",
					//	cam_now, pc_users[cam_now])
				}
				StopInlineMP4Playback(element)

			case "evt_subscribe":
				evt := dcMessage.Param
				iCam, err := strconv.Atoi(cam_now)
				if err == nil {
					iCam += 1
					addUserEventForCamera(caller, evt, fmt.Sprintf("%v", iCam))
				}
			case "get_archv":
				idx0 := dcMessage.Param // record index
				/*fmt.Printf("\r\ngetting rec list for cam %v starting at %v",
				cam_now, idx0)*/
				i_idx, err := strconv.Atoi(cam_now) //cam id offset by 1
				if err == nil {
					i_idx += 1
					next_idx := 0
					ip, err := getCameraIPById(fmt.Sprint(i_idx))
					if err == nil {
						i_idx, err = strconv.Atoi(idx0)
						if err == nil {
							ajax := `{"msg":"archv_data","rec":[`
							for i := i_idx; i < i_idx+10; i++ {
								if i != i_idx {
									ajax += ","
								}

								fn := getMP4_atIndex(ip, uint32(i))
								next_idx = i + 1
								//fmt.Printf("\r\nfile name===> %v", fn)
								if fn == "" {
									fmt.Printf("\r\n last index was at %v", i)
									next_idx = -1
									ajax += `"end"`
									break
								}
								ajax += `"` + fn + `"`
							}

							ajax = fmt.Sprintf(`%v],"lid":%v}`, ajax, next_idx)
							if d.ReadyState() == webrtc.DataChannelStateOpen {
								d.SendText(ajax)
								//fmt.Printf("%v", ajax)
							}

						}
					}
				}
			case "evt_unsubscribe":
				evt := dcMessage.Param
				deleteUserEventForCamera(caller, evt, cam_now)
			case "get_acd":
				bn, bt := get_cs_access_codes()
				//print("\r\ndc is now open for cam", cam_now)
				ajax := fmt.Sprintf(`{"msg":"set_ac","bn":"%v","bt":"%v"}`, bn, bt)
				if d.ReadyState() == webrtc.DataChannelStateOpen {
					d.SendText(ajax)
				}
				//fmt.Printf("\r\n--------%v", ajax)

			/*case "get_new_mp4_stream":
			go get_new_mp4_stream(d, dcMessage.Param)*/

			case "get_num_users":
				ajax := fmt.Sprintf(`{"msg":"num_users","count":%v}`, pc_users[cam_now])
				if d.ReadyState() == webrtc.DataChannelStateOpen {
					d.SendText(ajax)
				}

			case "get_vdo_cnt":
				iCam, err := strconv.Atoi(cam_now)
				if err == nil {
					iCam += 1

					ds := getAvailableDiskSpace()
					vcnt, vdos := getVideoCount(iCam)
					vdyo := strings.Join(vdos[:], ",")
					//fmt.Printf("\r\nvdos: %v", vdyo)
					ajax := fmt.Sprintf(`{"msg":"num_vdo","cnt":"%v","ds":"%v","vdos":"%v"}`,
						vcnt, ds, vdyo)
					if d.ReadyState() == webrtc.DataChannelStateOpen {
						d.SendText(ajax)
					}
				}
			case "set_access_code":
				var access_code AccessCode
				err := json.Unmarshal([]byte(dcMessage.Param), &access_code)
				if err == nil {
					fmt.Printf("net: %v; tok: %v", access_code.Tok, access_code.Net)
					change_bldg_access_codes(access_code.Tok, access_code.Net)
				}
				//change_bldg_access_codes
			case "get_cam_label":
				cid := dcMessage.Param
				lbl := getCamLabel(cid)
				//fmt.Printf("\r\nCam label for %s is %s", cid, lbl)
				ajax := fmt.Sprintf(`{"msg":"set_cam_label","param":"%s"}`, lbl)
				if d.ReadyState() == webrtc.DataChannelStateOpen {
					d.SendText(ajax)
				}
			case "next_cam_index":
				this_idx := dcMessage.Param
				i_idx, err := strconv.Atoi(this_idx)
				if err == nil {
					i_idx += 1
					_, err := getCameraIPById(fmt.Sprint(i_idx))
					if err == nil {
						//fmt.Printf("\r\nCurrent cam index %s; next index: %d", this_idx, i_idx)
						ajax := fmt.Sprintf(`{"msg":"next_cam_index","param":%d}`, i_idx)
						if d.ReadyState() == webrtc.DataChannelStateOpen {
							d.SendText(ajax)
						}
					}
				}

			case "set_cam_label":
				lbl := dcMessage.Param
				i_cam_now, err := strconv.Atoi(cam_now)
				if err == nil {
					i_cam_now += 1
					sz := fmt.Sprintf("%v", i_cam_now)
					changeCamSettings(sz, "x", lbl)
				}

			case "set_cs_ltln":
				ltln := dcMessage.Param
				fmt.Printf("\r\ncs_ltln => %v", ltln)
				// TODO: [important] => uuid must be set prior to ltln, otherwise this will fail
				change_cs_ltln(ltln)

			case "set_ltln":
				ltln := dcMessage.Param
				i_cam_now, err := strconv.Atoi(cam_now)
				if err == nil {
					i_cam_now += 1
					sz := fmt.Sprintf("%v", i_cam_now)
					fmt.Printf("\r\nltln for cam %v => %v", cam_now, ltln)
					changeCamSettings(sz, "4", ltln)
				}
			}
		}
	})
}
func runWebRTCLoop(from string, suuid string, sessionId string, sdp64 string) {
	Config.RunIFNotRun(suuid)
	codecs := Config.coGe(suuid)
	if codecs == nil {
		log.Println("Stream Codec Not Found")
		return
	}

	var AudioOnly bool
	if len(codecs) == 1 && codecs[0].Type().IsAudio() {
		AudioOnly = true
	}

	muxerWebRTC := NewMoxer(myOptions{ICEServers: Config.GetICEServers(),
		ICEUsername: Config.GetICEUsername(), ICECredential: Config.GetICECredential(),
		PortMin: Config.GetWebRTCPortMin(), PortMax: Config.GetWebRTCPortMax()})

	//var current_cam = suuid
	go webrtcInnerLoop(AudioOnly, suuid, muxerWebRTC)

	ReceivePeerConnection := func(pc *webrtc.PeerConnection, element *Moxer,
		mp4file string, caller string, uid string) {
		//log.Println("received peer connection")
		////peer = pc
		peerMutex.Lock()
		thisPeer := &Peer{peer: pc}
		PeerConn[sessionId] = thisPeer
		peerMutex.Unlock()

		// generate the 2 uuids here to be used as IDs for the 2 tracks
		muxerWebRTC.Set_UUID0(gen_uuid())
		muxerWebRTC.Set_UUID1(gen_uuid())

		pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
			element.SetStatus(connectionState)
		})

		pc.OnDataChannel(func(wdc *webrtc.DataChannel) {
			myWebRTC_Datachannel(caller, wdc, suuid, muxerWebRTC)
		})

		pcMap[caller+uid] = pc

	}
	mp4file := ""
	muxerWebRTC.WriteHeader(codecs, sdp64, mp4file,
		func(nIce *webrtc.ICECandidate) {

			var theIce = nIce.ToJSON().Candidate
			mytok := getUUID()
			sendIceCandidate(mytok, from, sessionId, theIce)

			// send ice candidates to requestor/cellphone
			////c.Writer.WriteString(theIce + "\r\n")
			////c.Writer.Flush()

			//log.Println(theIce)
		}, func(str webrtc.SessionDescription) {
			var newsdp = str.SDP

			mytok := getUUID()
			newsdp = strings.ReplaceAll(newsdp, "\r\n", "////")
			sendSDP(mytok, from, sessionId, newsdp)
		}, ReceivePeerConnection, from, suuid)

}

func processClientIce(sid string, candi string) {
	//print("the candidate: ", candi)
	cstring, err := base64.StdEncoding.DecodeString(candi)
	if err == nil && PeerConn[sid] != nil {
		var candy = webrtc.ICECandidateInit{Candidate: string(cstring)}
		//fmt.Printf("\r\npeerconn is: %v", PeerConn[sid])
		PeerConn[sid].peer.AddICECandidate(candy)
	}
}

// 123_94_12_0^
// 10.1.10.231
func processRelayedRequest(m map[string]interface{}) {
	from := m["mine"]
	req := m["request"]
	var sid interface{}

	//print("req is ", req.(string))
	// blessinguser -> for phpmyadmin

	switch req {
	case "cam_settings":
		go remote_change_cam_settings(m)
	case "get_cam_list":
		go remote_getCamList(from.(string))
	case "get_num_cams":
		ncams := 0
		print(ncams)
		// Alias /whatdfockisgonn /usr/share/phpmyadmin  --> for php my admin

	case "get_new_stream":
		sdp := m["data"]
		sid = m["sessionId"]
		camid, err := strconv.Atoi(m["camid"].(string))
		if err == nil {
			camid = camid - 1
			var suuid = fmt.Sprintf("%d", camid)
			/*fmt.Printf("\r\nWe got new stream request from %s for camid %s\n",
			from, suuid)*/
			runWebRTCLoop(from.(string), suuid, sid.(string), sdp.(string))

		}

	case "client_ice":
		//if got_sr {
		sid = m["sid"]
		ice := m["ice"]
		processClientIce(sid.(string), ice.(string))
		//}
	case "set_dev_token":
		a := m["dt"].(string)
		b := m["mine"].(string)
		c := m["dev"].(string)
		d := m["uzt"].(string)
		e := m["uph"].(string)
		addNewUserToken(a, b, c, d, e)
	}
}

func send_num_user_update(d *webrtc.DataChannel, idx string) {
	pc_users_sync.Lock()
	if d.ReadyState() == webrtc.DataChannelStateOpen {
		ajax := fmt.Sprintf(`{"msg":"num_user","cnt":"%v","cam":"%v"}`,
			pc_users[idx], idx)
		d.SendText(ajax)
	}
	pc_users_sync.Unlock()
}

func sendInlineJSON(d *webrtc.DataChannel, payload interface{}) {
	if d == nil || d.ReadyState() != webrtc.DataChannelStateOpen {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("inline mp4 marshal error: %v", err)
		return
	}
	d.SendText(string(data))
}
