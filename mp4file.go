package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pion/rtwatch/gst"
	"github.com/pion/webrtc/v2/pkg/media/ivfreader"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/oggreader"
)

var (
	mp4_peer = make(map[string](*webrtc.PeerConnection))
	mp4_dc   = make(map[string](*webrtc.DataChannel))
)

// readNextNAL extracts the next NAL unit (Annex-B start-code delimited)
func readNextNAL(r *bufio.Reader, startCode []byte) ([]byte, error) {
	var buf bytes.Buffer
	for {
		chunk, err := r.ReadBytes(0x01)
		if err != nil {
			return buf.Bytes(), err
		}
		buf.Write(chunk)
		if bytes.HasSuffix(buf.Bytes(), startCode) && buf.Len() > len(startCode) {
			break
		}
	}
	data := buf.Bytes()
	if len(data) > 4 {
		return data[:len(data)-4], nil
	}
	return data, nil
}

func streamFile(filename string, track *webrtc.TrackLocalStaticSample) error {
	cmd := exec.Command("ffmpeg",
		"-i", filename,
		"-an",             // no audio
		"-vcodec", "copy", // don’t re-encode
		"-f", "h264", // output raw H.264
		"-",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = nil // suppress logs
	if err := cmd.Start(); err != nil {
		return err
	}

	reader := bufio.NewReader(stdout)
	var frame []byte
	startCode := []byte{0x00, 0x00, 0x00, 0x01}

	for {
		nal, err := readNextNAL(reader, startCode)
		if len(nal) > 0 {
			frame = append([]byte{}, nal...)
			sample := media.Sample{
				Data:     frame,
				Duration: time.Second / 30,
			}
			if err := track.WriteSample(sample); err != nil {
				return err
			}
			time.Sleep(time.Second / 30)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("FFmpeg read error:", err)
			break
		}
	}

	_ = cmd.Wait()
	return nil
}

func getArchiveStartingAt(cam_now string, idx0 string) (ajax string) {
	//fmt.Printf("\r\nGetting archive for cam %v starting at index %v", cam_now, idx0)
	i_idx, err := strconv.Atoi(cam_now)
	ajax = ""
	if err == nil {
		i_idx += 1
		next_idx := 0
		ip, err := getCameraIPById(fmt.Sprint(i_idx))
		if err == nil {
			i_idx, err = strconv.Atoi(idx0)
			if err == nil {
				ajax = `{"msg":"archv_data","cam":"` + ip + `","rec":[`
				for i := i_idx; i < i_idx+9; i++ {
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

			}
		}
	}
	return ajax
}
func getMP4_atIndex(ip string, idx uint32) string {
	sFile := ""
	cnt := 0
	folder := "./recordings/" + ip + "/"
	pattern := ".mp4"
	files, err := os.ReadDir(folder)
	if err != nil {
		return sFile
	}
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), pattern) {
			if cnt == int(idx) {
				sFile = file.Name()
				break
			}
			cnt += 1
		}
	}
	return sFile
}

func LoadMP4(filename string) (*webrtc.TrackLocalStaticSample,
	*webrtc.TrackLocalStaticSample, *gst.Pipeline) {
	var err error
	videoTrack, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeH264,
	}, "Moxer.UUID0", "cashan-video")
	if err != nil {
		log.Fatal(err)
	}
	audioTrack, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "audio/opus"}, "synced", "audio")
	if err != nil {
		log.Fatal(err)
	}
	var pline = &gst.Pipeline{}
	pline = gst.CreatePipeline(filename, audioTrack, videoTrack)
	pline.Start()
	pline.Play()

	return videoTrack, audioTrack, pline

}

// Detect if the offer includes H264 or VP8 codecs for video.
// Returns "video/H264", "video/VP8", or "" if none.
func pickVideoCodec(sdp string) string {
	lo := strings.ToLower(sdp)
	// Prefer H.264 if present (often best interop with Safari).
	if strings.Contains(lo, "h264/90000") || strings.Contains(lo, "rtpmap:.*h264") {
		return "video/H264"
	}
	if strings.Contains(lo, "vp8/90000") || strings.Contains(lo, "rtpmap:.*vp8") {
		return "video/VP8"
	}
	return ""
}

func stream_from_mp4(mp4Path string, videoTrack, audioTrack *webrtc.TrackLocalStaticSample) {
	// Start ffmpeg for video (H264)
	videoCmd := exec.Command("ffmpeg",
		"-i", mp4Path,
		"-an", // disable audio
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-f", "h264",
		"-pix_fmt", "yuv420p",
		"pipe:1",
	)
	//videoCmd.Stderr = os.Stderr
	//videoCmd.Stdout = os.Stdout

	videoOut, err := videoCmd.StdoutPipe()
	if err != nil {
		//return fmt.Errorf("video stdout: %w", err)
	}
	if err := videoCmd.Start(); err != nil {
		//return fmt.Errorf("video start: %w", err)
	}

	// Start ffmpeg for audio (AAC to Opus)
	audioCmd := exec.Command("ffmpeg",
		"-i", mp4Path,
		"-vn",             // no video
		"-c:a", "libopus", // transcode audio to opus
		"-b:a", "32k",
		"-f", "opus", // output raw opus
		"pipe:1",
	)
	audioCmd.Stderr = os.Stderr
	audioOut, err := audioCmd.StdoutPipe()
	if err != nil {
		print(fmt.Errorf("audio stdout: %w", err))
	}
	if err := audioCmd.Start(); err != nil {
		print(fmt.Errorf("audio start: %w", err))
	}

	// Start video goroutine
	go func() {
		defer videoCmd.Wait()
		buf := make([]byte, 1400)
		for {
			/*if err := videoCmd.Wait(); err != nil {
				log.Printf("ffmpeg video process exited with error: %v", err)
				break
			}*/

			n, err := videoOut.Read(buf)
			if err == io.EOF {
				print("\r\n------------------------------video EOF?????")
				break
			} else if err != nil {
				log.Println("-----------------------video read error:", err)
				break
			}
			sample := media.Sample{
				Data:     append([]byte{}, buf[:n]...),
				Duration: time.Second / 30, // 30fps
			}
			if writeErr := videoTrack.WriteSample(sample); writeErr != nil {
				log.Println("video write error:", writeErr)
				break
			}
		}
		log.Println("video stream ended")
	}()

	// Start audio goroutine
	go func() {
		defer audioCmd.Wait()
		buf := make([]byte, 100)
		for {
			n, err := audioOut.Read(buf)
			if err == io.EOF {
				print("\r\n------------------------------Normal audio EOF found")
				break
			} else if err != nil {
				log.Println("audio read error:", err)
				break
			}
			sample := media.Sample{
				Data:     append([]byte{}, buf[:n]...),
				Duration: 20 * time.Millisecond, // typical Opus frame
			}
			if writeErr := audioTrack.WriteSample(sample); writeErr != nil {
				log.Println("audio write error:", writeErr)
				break
			}
		}
		log.Println("audio stream ended")
	}()

	//return nil
}

func drainRTCP(s *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		if _, _, err := s.Read(buf); err != nil {
			return
		}
	}
}

func setup_pc(videoCodec string, pc *webrtc.PeerConnection, indice string) webrtc.SessionDescription {
	var videoMime string
	mp4Path := "/home/oem/cashan/cashan-official-2025/camview/release/recordings/192.168.0.120/any_2025-09-01 19:24:46.mp4"
	switch videoCodec {
	case "video/H264":
		videoMime = webrtc.MimeTypeH264
	default:
		videoMime = webrtc.MimeTypeVP8
	}

	videoTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: videoMime},
		"video", "cashanv",
	)

	if err != nil {
		_ = pc.Close()
		return webrtc.SessionDescription{}
	}

	vSender, err := pc.AddTrack(videoTrack)
	if err != nil {
		_ = pc.Close()
		return webrtc.SessionDescription{}
	}

	go drainRTCP(vSender)

	audioTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"audio", "cashana",
	)

	if err != nil {
		_ = pc.Close()
		return webrtc.SessionDescription{}
	}
	aSender, err := pc.AddTrack(audioTrack)
	if err != nil {
		_ = pc.Close()
		return webrtc.SessionDescription{}
	}
	go drainRTCP(aSender)

	// Create and set local answer
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		return webrtc.SessionDescription{}
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		return webrtc.SessionDescription{}
	}

	pc.OnICECandidate(func(ik *webrtc.ICECandidate) {
		if ik != nil {
			fmt.Printf("\r\nWe got new local ice candidate %v", ik)
			theIce := ik.ToJSON().Candidate
			ajax := fmt.Sprintf("{\"msg\":\"mp4_ice\",\"param\":\"%v\"}", theIce)
			mp4_dc[indice].SendText(ajax)
		}
	})

	// When the ICE connection is established, start pushing media.
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("PC state: %s", s)
		switch s {
		case webrtc.PeerConnectionStateConnected:
			// Start ffmpeg pipelines once connected
			go pumpVideo(mp4Path, videoCodec, videoTrack)
			go pumpAudio(mp4Path, audioTrack)
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed,
			webrtc.PeerConnectionStateDisconnected:
			_ = pc.Close()
		}
	})
	return answer
}

func get_client_ice_mp4(sid_candi string) {

	// i need the pc to add ice candidate
	f2 := strings.Split(sid_candi, "////")
	indice := f2[1]
	candi := f2[2]

	//soundMap[ip] = append(soundMap[ip], float64(amp_float))
	//fmt.Printf("\r\nsid: %v", f2[1])
	ice, err := base64.StdEncoding.DecodeString(candi)
	if err == nil {
		fmt.Printf("\r\n is it good? %v", mp4_peer)
		var candy = webrtc.ICECandidateInit{Candidate: string(ice)}
		if _, exists := mp4_peer[indice]; !exists {
			return
		}
		//fmt.Printf("\r\npeerconn is: %v", PeerConn[sid])
		mp4_peer[indice].AddICECandidate(candy)
	}
}

func get_new_mp4_stream(dc *webrtc.DataChannel, sdp_str string) {
	fmt.Printf("\r\nnew mp4 stream!")

	isvr := Config.GetICEServers()
	var ices []webrtc.ICEServer

	str := strings.Split(sdp_str, "////")
	s := str[2] //remote sdp

	sdp := ""

	// Prepare codecs
	m := &webrtc.MediaEngine{}
	m.RegisterDefaultCodecs() // VP8/VP9/H264/Opus, etc.
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m))
	for index, sz := range isvr {
		if strings.HasPrefix(sz, "stun") {
			ices = append(ices, webrtc.ICEServer{URLs: []string{sz}})
		}
		fmt.Printf("\r\nIndex: %d, val: %s", index, sz)
	}
	//fmt.Printf("ice servers: %v", Config.GetICEServers())
	pc, err := api.NewPeerConnection(webrtc.Configuration{
		ICEServers: ices,
	})
	if err == nil {
		/*if _, exists := mp4_peer[str[1]]; !exists {
			mp4_peer[str[1]] = pc
		}*/
		mp4_peer[str[1]] = pc
		mp4_dc[str[1]] = dc

		_, _ = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
		_, _ = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})

		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return
		}
		s := string(b)

		//fmt.Printf("\r\noffer sdp: %v", s)
		offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: s}
		if err := pc.SetRemoteDescription(offer); err == nil {
			// Pick a video codec we can encode that the remote offered.
			videoCodec := pickVideoCodec(offer.SDP) // "video/H264" or "video/VP8"
			if videoCodec == "" {
				_ = pc.Close()
				return
			}
			//fmt.Printf("\r\nNegotiating video codec: %s", videoCodec)
			ssdp := setup_pc(videoCodec, pc, str[1])
			sdp = ssdp.SDP
		} else {
			fmt.Printf("\r\nSetRemoteDescription error: %v", err.Error())
			return
		}

	}

	sdp = strings.Replace(sdp, "\r\n", "////", -1)
	ajax := fmt.Sprintf("{\"msg\":\"mp4_sdp\",\"param\":\"%v\"}", sdp)
	/*
		ajax := fmt.Sprintf("{\"msg\":\"relay_msg\",\"mine\":\"%s\",\"hers\":\"%s\","+
			"\"request\":\"my_sdp\",\"sid\":\"%s\",\"sdp\":\"%s\"}", mine, hers, sid, sdp)
	*/

	if dc.ReadyState() == webrtc.DataChannelStateOpen {
		dc.SendText(ajax)
		//fmt.Printf("\r\n%v", ajax)
	}
}

func sendIceCandidate_mp4(sid string, candi string) {
	//ajax := fmt.Sprintf("{\"msg\":\"relay_msg\",\"mine\":\"%s\",\"hers\":\"%s\","+
	//	"\"request\":\"cs_candi\",\"sid\":\"%s\",\"candi\":\"%s\"}", mine, hers, sid, candi)
	//cherry_conn.WriteMessage(websocket.TextMessage, []byte(ajax))
}

func getVideoCount(iid int) uint32 {
	cnt := 0
	//fmt.Printf("\r\ngetting video count for cam %v", iid)
	ip, err := getCameraIPById(fmt.Sprintf("%v", iid))
	if err != nil {
		fmt.Printf("\r\nbig error: %v", err.Error())
		return 0
	}
	folder := "./recordings/" + ip + "/"
	pattern := ".mp4"
	//fmt.Printf("\r\nGetting .mp4 files for cam %v from folder '%v'", ip, folder)

	files, err := os.ReadDir(folder)
	//print(files)
	if err != nil {
		return 0
	}

	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), pattern) {
			cnt += 1
		}
	}
	return uint32(cnt)
}

func get_video_track_from_h264(h264 string) (track *webrtc.TrackLocalStaticSample, err error) {
	file, err2 := os.Open(h264) // Must contain Annex-B formatted H264
	//var vTrack = &webrtc.TrackLocalStaticSample{}
	if err2 != nil {
		return nil, err2
	}
	track, err = webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video", "stream",
	)

	reader := bufio.NewReader(file)
	nalPrefix := []byte{0x00, 0x00, 0x00, 0x01}
	ticker := time.NewTicker(time.Second / 30)
	defer ticker.Stop()

	buf := make([]byte, 0)
	go func() {
		for {
			// Read next NAL unit
			chunk, err := reader.ReadBytes(0x01) // crude delimiter; ideally parse NALs more robustly
			if err != nil {
				if err != io.EOF {
					fmt.Println("read error:", err)
					file.Close()
				}
				break
			}
			buf = append(buf, chunk...)

			<-ticker.C
			err = track.WriteSample(media.Sample{
				Data:     append(nalPrefix, chunk...),
				Duration: time.Second / 30,
			})
			if err != nil {
				fmt.Println("write error:", err)
				break
			}
			buf = buf[:0]
		}
	}()

	return track, err
}

// ----------------- Media pumpers (ffmpeg → readers → WriteSample) -----------------

func pumpVideo(path string, codec string, track *webrtc.TrackLocalStaticSample) {
	abs, _ := filepath.Abs(path)
	var cmd *exec.Cmd
	switch codec {
	case "video/H264":
		// Output Annex-B H.264 inside elementary stream is NOT directly consumable by Pion Sample API;
		// easiest: still use IVF/VP8 unless H264 is strictly required. For true H264, use Pion's H264 payloader
		// or rely on ffmpeg to packetize? Simpler route: MP4->H264 in Annex-B, then wrap bytes per frame as samples.
		// Many browsers accept H264 even if marked as samples; this works for simple demo streams.
		cmd = exec.Command("ffmpeg",
			"-re",
			"-stream_loop", "-1",
			"-i", abs,
			"-an",
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-tune", "zerolatency",
			"-profile:v", "baseline",
			"-level", "3.1",
			"-x264-params", "scenecut=0:open_gop=0:repeat-headers=1:force-cfr=1", //"keyint=60:min-keyint=60:scenecut=0",
			"-bf", "0",
			"-force_key_frames", "expr:gte(t,0)", // immediate IDR at start
			"-force_key_frames", "expr:gte(t,n_forced*1)", // then every 1 second
			"-f", "h264", // Annex-B
			"-",
		)
	default: // VP8
		cmd = exec.Command("ffmpeg",
			"-re", "-i", abs,
			"-an",
			"-c:v", "libvpx",
			"-deadline", "realtime",
			"-cpu-used", "5",
			"-b:v", "1500k",
			"-f", "ivf",
			"-bf", "0",
			"-force_key_frames", "expr:gte(t,0)", // immediate IDR at start
			"-force_key_frames", "expr:gte(t,n_forced*1)", // then every 1 second
			"-",
		)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("video pipe: %v", err)
		return
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("video start: %v", err)
		return
	}

	if codec == "video/VP8" {
		reader, header, err := ivfreader.NewWith(stdout)
		if err != nil {
			log.Printf("ivf open: %v", err)
			_ = cmd.Process.Kill()
			return
		}
		frameDur := time.Duration(float64(time.Second) * float64(header.TimebaseNumerator) / float64(header.TimebaseDenominator))
		for {
			frame, _, err := reader.ParseNextFrame()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				log.Printf("ivf read: %v", err)
				return
			}
			if werr := track.WriteSample(media.Sample{Data: frame, Duration: frameDur}); werr != nil {
				log.Printf("video write: %v", werr)
				return
			}
		}
	} else {
		// H.264 Annex-B stream: write each NALU group as a "sample".
		// Very naive splitter: chunk by AUD or IDR boundaries would be better.
		// For demo purposes, read in chunks and write at ~33ms.
		buf := make([]byte, 4096)
		chunk := make([]byte, 0, 1<<16)
		ticker := time.NewTicker(33 * time.Millisecond)
		defer ticker.Stop()
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				chunk = append(chunk, buf[:n]...)
			}
			if err == io.EOF {
				return
			}
			if err != nil {
				log.Printf("h264 read: %v", err)
				return
			}
			select {
			case <-ticker.C:
				if len(chunk) > 0 {
					if werr := track.WriteSample(media.Sample{Data: chunk, Duration: 33 * time.Millisecond}); werr != nil {
						log.Printf("h264 write: %v", werr)
						return
					}
					chunk = chunk[:0]
				}
			default:
			}
		}
	}
}

/*go*/
/*func read_rtp_from_udp_and_write_to_track() {
	time.Sleep(500 * time.Millisecond)

	conn, err := net.ListenPacket("udp", "127.0.0.1:5004")
	if err != nil {
		log.Println("UDP listen error:", err)
		return
	}
	defer conn.Close()

	log.Println("Listening for RTP on udp/127.0.0.1:5004 ...")
	buf := make([]byte, 1500)
	p := &rtp.Packet{}
	packets := 0

	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			log.Println("UDP read error:", err)
			return
		}
		if err := p.Unmarshal(buf[:n]); err != nil {
			continue
		}

		// Force PT=96 in case sender differs
		p.PayloadType = 96

		if err := videoTrack.WriteRTP(p); err != nil {
			log.Println("WriteRTP error:", err)
			return
		}

		packets++
		if packets%120 == 0 {
			log.Printf("Forwarded %d RTP packets (ts=%d, seq=%d, m=%v)\n",
				packets, p.Timestamp, p.SequenceNumber, p.Marker)
		}
	}
}*/

func pumpAudio(path string, track *webrtc.TrackLocalStaticSample) {
	abs, _ := filepath.Abs(path)
	cmd := exec.Command("ffmpeg",
		"-re", "-i", abs,
		"-vn",
		"-ac", "2",
		"-ar", "48000",
		"-c:a", "libopus",
		"-b:a", "96k",
		"-application", "lowdelay",
		"-frame_duration", "20",
		"-f", "ogg",
		"-",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("audio pipe: %v", err)
		return
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("audio start: %v", err)
		return
	}

	ogg, _, err := oggreader.NewWith(stdout)
	if err != nil {
		log.Printf("ogg open: %v", err)
		_ = cmd.Process.Kill()
		return
	}
	frameDur := 20 * time.Millisecond
	for {
		p, _, err := ogg.ParseNextPage()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			log.Printf("ogg read: %v", err)
			return
		}
		if werr := track.WriteSample(media.Sample{Data: p, Duration: frameDur}); werr != nil {
			log.Printf("audio write: %v", werr)
			return
		}
	}
}

/*func get_audio_and_video_tracks_from_mp4(filename string) (vdo_track,
	aud_track webrtc.TrackLocal, err error) {
	vdo_track, err = webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video", "mp4video",
	)
	if err != nil {
		return nil, nil, err
	}

	// create aac audio track
	aud_track, err = webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "mp4audio",
	)
	if err != nil {
		return nil, nil, err
	}

	// Start ffmpeg: one process for both audio and video using different pipes
	videoPipeR, videoPipeW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	//audioPipeR, audioPipeW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command("ffmpeg",
		"-re",
		"-i", filename,
		"-map", "0:v:0", "-c:v", "copy", "-bsf:v", "h264_mp4toannexb", "-f", "h264", "pipe:3",
		//"-map", "0:a:0?", "-c:a", "copy", "-f", "adts", "pipe:4",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{videoPipeW} //, audioPipeW}

	err = cmd.Start()
	if err != nil {
		return nil, nil, err
	}

	// VIDEO: read from videoPipeR
	go func() {
		defer videoPipeR.Close()
		reader := bufio.NewReader(videoPipeR)
		nalPrefix := []byte{0x00, 0x00, 0x00, 0x01}
		//buf := make([]byte, 0)
		ticker := time.NewTicker(time.Second / 30)

		for {
			nal, err := reader.ReadBytes(0x01)
			if err != nil {
				fmt.Printf("\r\ncan't read bytes from mp4")
				break
			}

			<-ticker.C
			videoTrack.WriteSample(media.Sample{
				Data:     append(nalPrefix, nal...),
				Duration: time.Second / 30,
			})
		}
	}()

	// AUDIO: read from audioPipeR


	return vdo_track, aud_track, err
}


here is how you get stream from mp4 file to PC

		pc.OnDataChannel(func(d *webrtc.DataChannel) {
			d.OnMessage(func(msg webrtc.DataChannelMessage) {
					var camfile = strings.Replace(m, "show_recording_", "", 1)
					log.Println("Showing recording for", camfile)
					dir, _ := os.Getwd()
					var fullpath = dir + "/capture/" + camfile
					//print("fullpath:", fullpath)
					lastTrack = fullpath

					vdo, aud := LoadMP4(fullpath)
					element.SetMP4(camfile)
					var senders = pc.GetSenders()
					var snd_vdo = senders[0]
					var snd_aud = senders[1]

					snd_vdo.ReplaceTrack(vdo)
					snd_aud.ReplaceTrack(aud)

			}) // d.OnMessage
		}) // pc.OnDataChannel
*/
