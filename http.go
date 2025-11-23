package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/deepch/vdk/av"
	"github.com/gofrs/uuid"

	webX "github.com/deepch/vdk/format/webrtcv3"
	"github.com/gin-gonic/gin"
	"github.com/pion/webrtc/v3"
)

var (
	cam_is_public    bool   = false
	this_cam_station string = ""
	// really users should have just one public cam
)

type JCodec struct {
	Type string
}

type Stream struct {
	codec av.CodecData
	track *webrtc.TrackLocalStaticSample
}

type Options struct {
	// ICEServers is a required array of ICE server URLs to connect to (e.g., STUN or TURN server URLs)
	ICEServers []string
	// ICEUsername is an optional username for authenticating with the given ICEServers
	ICEUsername string
	// ICECredential is an optional credential (i.e., password) for authenticating with the given ICEServers
	ICECredential string
	// ICECandidates sets a list of external IP addresses of 1:1
	ICECandidates []string
	// PortMin is an optional minimum (inclusive) ephemeral UDP port range for the ICEServers connections
	PortMin uint16
	// PortMin is an optional maximum (inclusive) ephemeral UDP port range for the ICEServers connections
	PortMax uint16
}
type ServerConfig struct {
	uuid       string
	owner1     string
	lat        string
	lng        string
	guests     map[string]map[string]string
	sharedCams map[string]string
	cam_list   []string
	cam_config map[string]map[string]string
	gt         string
}

const (
	APP_ID    = "cdd8d225e899f6c5752b2df42329f0a6"
	API_BASE  = "https://rtc.live.cloudflare.com/v1/apps/" + APP_ID
	APP_TOKEN = "c8ad72b6c4f704424a889357ffd9ecb1af0c6a7bbb9c4b8f2070b3ec801a19e2"
)

func getPCForCF(pc *webrtc.PeerConnection, mx *webX.Muxer, suuid string) {
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		log.Println("dunno why but...")
	} else {
		pc.SetLocalDescription(offer)
		/*pc.AddTrack(mx.GetVideoTrack())
		pc.AddTrack(mx.GetAudioTrack())
		trans := pc.GetTransceivers()
		log.Println("tkid-0:", trans[0].Sender().Track().ID())*/
	}
	log.Println("creating OnIceCandidate event")
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			log.Println("received i/c for CF")
		}
	})
}

func setCamPublic(b bool) {
	cam_is_public = b
}

func gen_uuid() (suid string) {
	uid, _ := uuid.NewV7()
	suid = uid.String()
	//log.Println("suid is ", suid)
	return suid
}

func HTTPAPIGetCloudFareIceServers(c *gin.Context) {
	myurl := `-X POST -H "Authorization: Bearer 4e9e11376d07d0bc9cf3e4740d9e4127350dad8f8e4d3d5ddd3ac7f00aea336e" -H "Content-Type: application/json" -d '{"ttl": 86400}' https://rtc.live.cloudflare.com/v1/turn/keys/996b93e5c9835944e06dca67323fb0d8/credentials/generate`

	out, err := exec.Command("curl " + myurl).Output() //+ myurl).Output()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("response from server: " + string(out))
}

func HTTPAPIServerIndex(c *gin.Context) {
	_, all := Config.list()
	if len(all) > 0 {
		c.Header("Cache-Control", "no-cache, max-age=0, must-revalidate, no-store")
		c.Header("Access-Control-Allow-Origin", "*")
		c.Redirect(http.StatusMovedPermanently, "stream/player/"+all[0])
	} else {
		c.HTML(http.StatusOK, "index.tmpl", gin.H{
			"port":    Config.Server.HTTPPort,
			"version": time.Now().String(),
		})
	}
}

// HTTPAPIServerStreamPlayer stream player
func HTTPAPIServerStreamPlayer(c *gin.Context) {
	_, all := Config.list()
	sort.Strings(all)
	c.HTML(http.StatusOK, "player.htm", gin.H{
		"port":     Config.Server.HTTPPort,
		"suuid":    c.Param("uuid"),
		"suuidMap": all,
		"version":  time.Now().String(),
	})
}

func HTTPAPIPostIceCandis(c *gin.Context) {
	//log.Println("HTTPAPIPostIceCandis called")
	var candi = c.PostForm("candi")
	var sender = c.PostForm("sender")
	var suuid = c.PostForm("id")
	var server = pcMap[sender+suuid]
	if server == nil {
		//log.Println("got candis but server[", sender+suuid, "] is nil")
		for {
			server = pcMap[sender+suuid]
			if server != nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		//log.Println("server is now not nil")
	}

	//log.Println("adding candis", candi)
	var candy = webrtc.ICECandidateInit{Candidate: string(candi)}
	server.AddICECandidate(candy)
	//log.Println(candi)

}

func CreatePC_ForCF(muxer *Moxer) *webrtc.PeerConnection {
	return muxer.CreatePC()
}

func UploadOfferToCF(sid string, offer webrtc.SessionDescription,
	avid string, audid string, cam_station string, camId string) string {
	//params := url.Values{}
	tracks := []interface{}{
		map[string]interface{}{
			"location":  "local",
			"mid":       "0",
			"trackName": avid,
		},
		map[string]interface{}{
			"location":  "local",
			"mid":       "1",
			"trackName": audid,
		},
	}
	payload := map[string]interface{}{
		"sessionDescription": map[string]interface{}{
			"sdp":  offer.SDP,
			"type": "offer",
		},
		"tracks": tracks,
	}
	sjson, _ := json.Marshal(payload)

	url := API_BASE + "/sessions/" + sid + "/tracks/new"
	//log.Println(url)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(sjson))
	if err != nil {
		log.Fatal(err)
	}

	//req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+APP_TOKEN) // Example of another header

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	cf_response, _ := io.ReadAll(resp.Body)

	//log.Println(string(body_response))

	//////////////////////////////////////////////////////////

	// now make available on cashan.net
	tracks2 := []interface{}{
		map[string]interface{}{
			"location":  "remote",
			"sessionId": sid,
			"trackName": avid,
		},
		map[string]interface{}{
			"location":  "remote",
			"sessionId": sid,
			"trackName": audid,
		},
	}
	jd := `{"action":"set"}`
	buf := []byte(jd)
	sjson, _ = json.Marshal(tracks2)
	uri := "https://cashan.net/c3/calls/cfnew.php?a=set&tko=" + string(sjson) +
		"&uid=" + cam_station + "&c=" + camId
	//log.Println("uri is", uri)
	req, _ = http.NewRequest("POST", uri, bytes.NewBuffer(buf))
	req.Header.Set("Content-type", "application/json")
	cli := &http.Client{}
	resp, err = cli.Do(req)
	if err != nil {
		log.Fatal(err)
	}

	defer resp.Body.Close()
	//cashan_response, _ := io.ReadAll(resp.Body)
	//log.Println("Result from'", uri, "'", string(cashan_response))

	return string(cf_response)

}

func WritePacket_cloudflare(muxerWebRTC *Moxer, current_cam string) {
	go func() {
		is_running := false
		//log.Println("current cam is ", current_cam)
		cid, ch := Config.clAd(current_cam)
		muxerWebRTC.SetCurrentCam(current_cam)
		defer Config.clDe(current_cam, cid)
		defer muxerWebRTC.Close()
		var videoStart bool
		noVideo := time.NewTimer(5 * time.Second)
		for {
			select {
			case <-noVideo.C:
				log.Println("(cf)noVideo on cam", current_cam, "reconnecting with cam")
				Config.RunIFNotRun(current_cam)
				//return
			case pck := <-ch:
				if pck.IsKeyFrame {
					noVideo.Reset(10 * time.Second) // this was originally 10 b4 cashan
					videoStart = true
				}
				if !videoStart {
					//log.Println("video has not yet started")
					Config.RunIFNotRun(current_cam)
					continue
				}
				//if muxerWebRTC.GetCurrentCam() == current_cam {
				_ = muxerWebRTC.WritePacket(pck)
				if !is_running {
					is_running = true
					//log.Println("Start WritePacket", current_cam)
				}
				/*if err != nil || !cam_is_public {
					//log.Println("Either cf disconnected OR cam", current_cam, "no longer public")
					RemoveCamFromServer(current_cam)
					return
				}*/
			}
		}
	}()
}

func RemoveCamFromServer(camId string) {
	uri := "https://cashan.net/c3/calls/cfnew.php?a=del&tko=" +
		"&uid=" + this_cam_station + "&c=" + camId
	req, _ := http.NewRequest("POST", uri, nil)
	req.Header.Set("Content-type", "application/json")
	cli := &http.Client{}
	resp, err := cli.Do(req)
	if err != nil {
		log.Fatal(err)
	}

	defer resp.Body.Close()
	/*cashan_response, _ := io.ReadAll(resp.Body)
	log.Println("Result from'", url, "'", string(cashan_response))*/
}

func Connect_Cloudflare(active_muxer *Moxer, codecs []av.CodecData,
	cam_station string, camId string) {
	go func() { //running asynchronously
		cf_session := CreateCallSession()
		//log.Println("cf session is:", cf_session)
		pc := CreatePC_ForCF(active_muxer)

		/////////////////////////////////////////////////

		active_muxer.Set_UUID0(gen_uuid())
		active_muxer.Set_UUID1(gen_uuid())

		active_muxer.TransmitActualTracks(codecs, pc)

		pc.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
			active_muxer.SetStatus(connectionState)
			//log.Println("cloudfare connectionState is:", connectionState)
		})

		/////////////////////////////////////////////////

		avTrack := active_muxer.GetVideoTrack()
		auTrack := active_muxer.GetAudioTrack()
		log.Println("video:", avTrack)
		log.Println("audio:", auTrack)

		WritePacket_cloudflare(active_muxer, camId) // for now cam id is hard coded to 0

		offer, err := pc.CreateOffer(nil)
		//log.Println("cloudflare tracks id:", avTrack.ID(), auTrack.ID())
		if err == nil {
			pc.SetLocalDescription(offer)
			sAnswer := UploadOfferToCF(cf_session, offer, avTrack.ID(),
				auTrack.ID(), cam_station, camId)

			var jMap map[string]interface{}
			json.Unmarshal([]byte(sAnswer), &jMap)

			sMap, _ := json.Marshal(jMap["sessionDescription"])
			json.Unmarshal([]byte(sMap), &jMap)

			kMap := jMap["sdp"]

			remote_answer := webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer,
				SDP:  string(fmt.Sprint(kMap)),
			}
			//log.Println("cf answer", remote_answer)
			//sdesc := webrtc.SessionDescription{Type: "answer", SDP: kMap}
			pc.SetRemoteDescription(remote_answer)
		} else {
			log.Println("cant create offer??")
		}

		//active_muxer
	}()

}

func CreateCallSession() string {
	url := API_BASE + "/sessions/new"

	req, err := http.NewRequest("POST", url, nil) //bytes.NewBuffer(jsonData))
	if err != nil {
		log.Fatal(err)
	}

	//req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+APP_TOKEN) // Example of another header

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	var j map[string]interface{} //interface{}
	json.NewDecoder(resp.Body).Decode(&j)
	sid, ok := j["sessionId"].(string)
	if !ok {
		panic("sid is not a string??")
	}

	//log.Println(sid)

	return sid
}
func HTTPAPILaunchConnectCamToCloudfare(c *gin.Context) {
}

func NotifyCashanOfClientEvent(evt string, requestor string, vdoid string) {
	url := "https://cashan.net/c3/calls/client-add.php?p=" + evt + "&r=" + requestor + "&v=" + vdoid

	req, _ := http.NewRequest(http.MethodGet, url, nil)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	//var j map[string]interface{} //interface{}
	//json.NewDecoder(resp.Body).Decode(&j)
	//sid, ok := j["sessionId"].(string)

	log.Println(string(body), resp.Status)

}

// this function duplicates instructions from HTTPAPIServerStreamWebRTC( )
// with the difference that this function setups connection to cloudflare
func StartCF_Thread(suuid string, cam_station string) { //c *gin.Context) {
	log.Println("Launching cf thread for cam", suuid)
	go func() {
		//var suuid = c.PostForm("suuid")
		//var requestor = c.PostForm("requestor")
		//var cam_now = suuid
		//cam_station := c.PostForm("cs")

		Config.RunIFNotRun(suuid)
		codecs := Config.coGe(suuid)
		if codecs == nil {
			log.Println("Stream Codec Not Found")
			return
		}

		//////

		//var thePC webrtc.PeerConnection
		/*muxerWebRTC := NewMoxer(webX.Options{ICEServers: Config.GetICEServers(),
			ICEUsername: Config.GetICEUsername(), ICECredential: Config.GetICECredential(),
			PortMin: Config.GetWebRTCPortMin(), PortMax: Config.GetWebRTCPortMax()})

		// for now there is no data channel with cloudflare
		Connect_Cloudflare(muxerWebRTC, codecs, cam_station, suuid)*/
	}()
}

func GetConfigFileContent(filename string) map[string]interface{} {
	var f interface{}
	file, _ := os.ReadFile(filename)

	json.Unmarshal(file, &f)
	m := f.(map[string]interface{})
	return m
}

func GetCamParameter(param string, camId string) string {
	m := GetConfigFileContent("server.json")
	var mp interface{} = m["cam_config"]
	s := mp.(map[string]interface{})
	counter := 0
	ret := ""
	for k, _ := range s {
		//log.Println("now / counter", cam_now, "/", counter)
		if camId == fmt.Sprint(counter) {
			elm := s[k].(map[string]interface{})
			ret = fmt.Sprint(elm[param])
			break
		}
		counter += 1
	}
	return ret
}

func OnDataChannel(d *webrtc.DataChannel, cam_now string, element *Moxer) {
	d.OnMessage(func(msg webrtc.DataChannelMessage) {
		/*var m = string(msg.Data)

		if strings.HasPrefix(m, "get_mp4s_") {
			var cam = strings.Replace(m, "get_mp4s_", "", 1)
			//log.Println("cam is", cam)

			ex, _ := os.Executable()
			exPath := filepath.Dir(ex) + "/capture/"

			files, _ := os.ReadDir(exPath)
			//log.Println(files)
			var rec = "["
			var cnt = 0
			for _, file := range files {
				//log.Println(file.Name())
				if strings.HasPrefix(file.Name(), "cam"+cam+"_") {
					/*if cnt > 0 {
						rec += ","
					}
					rec += "\"" + file.Name() + "\""
					cnt = cnt + 1

					//log.Println(file.Name())
				}
			}
			rec += "]"
			d.SendText("mp4_count_" + cam + ":" + fmt.Sprint(cnt))
			//log.Println("Camera", cam, "has", cnt, "recordings")
			//log.Println("get mp4s for cam", cam, ":", cnt)

		} else if strings.HasPrefix(m, "switch_cam_") {
			element.SetMP4("none")

			var cam = strings.Replace(m, "switch_cam_", "", 1)
			if cam == cam_now {
				//log.Println(cam, "is already active cam. Ignoring swap request")
				return
			}

			cam_now = cam
			element.SetCurrentCam(cam)
			//log.Println("replacing current camera with ", cam)

		} else if strings.HasPrefix(m, "is_vdo_public") {

			m := GetConfigFileContent("server.json")
			var mp interface{} = m["cam_config"]
			s := mp.(map[string]interface{})
			counter := 0
			for k, _ := range s {
				//log.Println("now / counter", cam_now, "/", counter)
				if fmt.Sprint(cam_now) == fmt.Sprint(counter) {
					pub := s[k].(map[string]interface{})
					//log.Println("checking if cam w/ ip", k, "is public")
					//log.Println("is it?", pub["public"])
					//log.Println(k, s[k])
					d.SendText("is_public:" + fmt.Sprint(pub["public"]))
					break
				}
				counter += 1
			}
			//log.Println("s is ", s)
		} else if strings.HasPrefix(m, "make_public") {
			// this is the first chance we have to create live_stream
			// entry for this camera
			StartCF_Thread(cam_now, this_cam_station)
			d.SendText("made_public")
			setCamPublic(true)
			//server_json := GetConfigFileContent("server.json")
		} else if strings.HasPrefix(m, "end_public") {
			server_json := GetConfigFileContent("server.json")
			log.Println("Ending public availability for camera", cam_now)
			log.Println(server_json)
			d.SendText("ended_public")
			element.Close()
		}*/

	}) // d.OnMessage
}

func HTTPLocalRequest(c *gin.Context) {
	var key = c.PostForm("key")
	var val = c.PostForm("value")
	switch key {
	case "token":
		this_cam_station = val
		log.Println("cam station set to ", val)
	case "unshare":
		setCamPublic(false)
		log.Println("cam no longer public")
	case "launchcam":
		time.Sleep(3 * time.Second)
		log.Println("after reboot python notified us there should be a live_stream on cam #", val)
		StartCF_Thread(val, this_cam_station)
	}
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Headers", "Origin, X-Requested-With, Content-Type, Accept, Authorization, x-access-token")
		c.Header("Access-Control-Expose-Headers", "Content-Length, Access-Control-Allow-Origin, Access-Control-Allow-Headers, Cache-Control, Content-Language, Content-Type")
		c.Header("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

type Response struct {
	Tracks []string `json:"tracks"`
	Sdp64  string   `json:"sdp64"`
}

type ResponseError struct {
	Error string `json:"error"`
}
