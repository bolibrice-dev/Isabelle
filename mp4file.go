package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v2/pkg/media/ivfreader"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/oggreader"
)

var (
	mp4_peer         = make(map[string](*webrtc.PeerConnection))
	mp4_dc           = make(map[string](*webrtc.DataChannel))
	mp4_playbacks    = make(map[string]*mp4Playback)
	mp4PlaybackMu    sync.Mutex
	mp4TestServePath string
	mp4PathMu        sync.RWMutex
)

const recordingsRoot = "./recordings"

//go:embed web/mp4test.html
var mp4TestPageHTML string

type mp4Playback struct {
	path         string
	videoCodec   string
	videoProfile string
	videoLevel   string

	videoTrack *webrtc.TrackLocalStaticSample
	audioTrack *webrtc.TrackLocalStaticSample

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

func newMP4Playback(path string, videoCodec string, videoTrack, audioTrack *webrtc.TrackLocalStaticSample, videoProfile, videoLevel string) (*mp4Playback, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("mp4 playback open %q: %w", path, err)
	}
	if videoCodec == "" {
		videoCodec = webrtc.MimeTypeH264
	}

	if videoTrack == nil {
		var err error
		videoTrack, err = webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: videoCodec},
			fmt.Sprintf("mp4-%d", time.Now().UnixNano()),
			"mp4-video",
		)
		if err != nil {
			return nil, fmt.Errorf("mp4 playback video track: %w", err)
		}
	}
	if audioTrack == nil {
		var err error
		audioTrack, err = webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
			fmt.Sprintf("mp4-%d", time.Now().UnixNano()),
			"mp4-audio",
		)
		if err != nil {
			return nil, fmt.Errorf("mp4 playback audio track: %w", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &mp4Playback{
		path:         path,
		videoCodec:   videoCodec,
		videoProfile: videoProfile,
		videoLevel:   videoLevel,
		videoTrack:   videoTrack,
		audioTrack:   audioTrack,
		ctx:          ctx,
		cancel:       cancel,
	}, nil
}

func currentMP4ServePath() string {
	mp4PathMu.RLock()
	defer mp4PathMu.RUnlock()
	return mp4TestServePath
}

func recordingsRootAbs() (string, error) {
	abs, err := filepath.Abs(recordingsRoot)
	if err != nil {
		return "", fmt.Errorf("recordings root: %w", err)
	}
	return abs, nil
}

func listAvailableMP4Files(root string) ([]string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	entries := make([]string, 0, 16)
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".mp4") {
			rel, err := filepath.Rel(absRoot, path)
			if err != nil {
				return err
			}
			entries = append(entries, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	return entries, nil
}

func resolveRecordingPath(clientPath string) (string, error) {
	clean := strings.TrimSpace(clientPath)
	if clean == "" {
		return "", fmt.Errorf("mp4 path: empty path")
	}
	clean = filepath.Clean(clean)
	absRoot, err := recordingsRootAbs()
	if err != nil {
		return "", err
	}
	var absPath string
	if filepath.IsAbs(clean) {
		absPath = clean
	} else {
		absPath = filepath.Join(absRoot, clean)
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return "", err
	}
	prefix := absRoot + string(os.PathSeparator)
	if absPath == absRoot || !strings.HasPrefix(absPath, prefix) {
		return "", fmt.Errorf("mp4 path: %q outside recordings directory", clientPath)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("mp4 path: %q is a directory", clientPath)
	}
	return absPath, nil
}

func relativeRecordingPath(absPath string) string {
	if strings.TrimSpace(absPath) == "" {
		return ""
	}
	absRoot, err := recordingsRootAbs()
	if err != nil {
		return absPath
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return absPath
	}
	if rel == "." {
		return ""
	}
	if strings.HasPrefix(rel, "..") {
		return absPath
	}
	return filepath.ToSlash(rel)
}

func currentMP4RelativePath() string {
	return relativeRecordingPath(currentMP4ServePath())
}

// SwitchMP4PlaybackFile updates the MP4 file used for testing/streaming.
func normalizeMP4FilePath(newPath string) (string, error) {
	cleanPath := strings.TrimSpace(newPath)
	if cleanPath == "" {
		return "", fmt.Errorf("mp4 switch: empty path")
	}
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("mp4 switch: resolve %q: %w", newPath, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("mp4 switch: stat %q: %w", absPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("mp4 switch: %q is a directory", absPath)
	}
	return absPath, nil
}

func SwitchMP4PlaybackFile(newPath string) error {
	absPath, err := normalizeMP4FilePath(newPath)
	if err != nil {
		return err
	}
	mp4PathMu.Lock()
	mp4TestServePath = absPath
	mp4PathMu.Unlock()
	return nil
}

// SwapPeerConnectionPlayback reuses an existing PeerConnection's tracks and starts streaming a new MP4 file.
// It stops the current playback, replaces it with a new mp4Playback that writes to the same tracks, and restarts.
func SwapPeerConnectionPlayback(indice string, newPath string) error {
	absPath, err := normalizeMP4FilePath(newPath)
	if err != nil {
		fmt.Printf("\r\nnewpath error: %v", err)
		return err
	}
	key := "pc:" + indice

	mp4PlaybackMu.Lock()
	current, ok := mp4_playbacks[key]
	mp4PlaybackMu.Unlock()
	if !ok || current == nil {
		return fmt.Errorf("mp4 swap: no active playback for %s", indice)
	}

	videoTrack := current.VideoTrack()
	audioTrack := current.AudioTrack()
	videoCodec := current.videoCodec
	videoProfile := current.videoProfile
	videoLevel := current.videoLevel

	current.Stop()

	newPlayback, err := newMP4Playback(absPath, videoCodec, videoTrack, audioTrack, videoProfile, videoLevel)
	if err != nil {
		return fmt.Errorf("mp4 swap: %w", err)
	}

	storeMP4Playback(key, newPlayback)
	newPlayback.Start()
	return nil
}

func (m *mp4Playback) VideoTrack() *webrtc.TrackLocalStaticSample {
	return m.videoTrack
}

func (m *mp4Playback) AudioTrack() *webrtc.TrackLocalStaticSample {
	return m.audioTrack
}

func (m *mp4Playback) Start() {
	m.once.Do(func() {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			pumpVideo(m.ctx, m.path, m.videoCodec, m.videoProfile, m.videoLevel, m.videoTrack)
		}()

		if m.audioTrack != nil {
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				pumpAudio(m.ctx, m.path, m.audioTrack)
			}()
		}
	})
}

func (m *mp4Playback) Stop() {
	m.cancel()
	m.wg.Wait()
}

func stopMP4Playback(key string) {
	mp4PlaybackMu.Lock()
	defer mp4PlaybackMu.Unlock()
	if playback, ok := mp4_playbacks[key]; ok {
		playback.Stop()
		delete(mp4_playbacks, key)
	}
}

func storeMP4Playback(key string, playback *mp4Playback) {
	mp4PlaybackMu.Lock()
	defer mp4PlaybackMu.Unlock()
	if existing, ok := mp4_playbacks[key]; ok {
		existing.Stop()
	}
	if playback == nil {
		delete(mp4_playbacks, key)
		return
	}
	mp4_playbacks[key] = playback
}

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
	*webrtc.TrackLocalStaticSample, *mp4Playback, error) {
	videoTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeH264,
	}, "Moxer.UUID0", "cashan-video")
	if err != nil {
		return nil, nil, nil, err
	}
	audioTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"synced", "audio",
	)
	if err != nil {
		return nil, nil, nil, err
	}

	playback, err := newMP4Playback(filename, webrtc.MimeTypeH264, videoTrack, audioTrack, "", "")
	if err != nil {
		return nil, nil, nil, err
	}
	return videoTrack, audioTrack, playback, nil
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

func drainRTCP(s *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		if _, _, err := s.Read(buf); err != nil {
			return
		}
	}
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

func getVideoCount(iid int) (uint32, []string) {
	cnt := 0
	//fmt.Printf("\r\ngetting video count for cam %v", iid)
	ip, err := getCameraIPById(fmt.Sprintf("%v", iid))
	if err != nil {
		fmt.Printf("\r\nbig error: %v", err.Error())
		return 0, nil
	}

	fldr := fmt.Sprintf("%v/%v", recordingsRoot, ip)
	fles, err := listAvailableMP4Files(fldr)
	resp := mp4ListResponse{
		Files:   fles,
		Current: currentMP4RelativePath(),
	}
	if err != nil {
		fmt.Printf("\r\nfiles resp: %v", len(resp.Files))
		return 0, nil
	}
	cnt = len(resp.Files)

	return uint32(cnt), resp.Files
}

// ----------------- Media pumpers (ffmpeg → readers → WriteSample) -----------------

func pumpVideo(ctx context.Context, path string, codec string, profile string, level string, track *webrtc.TrackLocalStaticSample) {
	abs, _ := filepath.Abs(path)
	var cmd *exec.Cmd
	switch codec {
	case "video/H264":
		prof := profile
		if prof == "" {
			prof = "baseline"
		}
		lvl := level
		if lvl == "" {
			lvl = "3.1"
		}
		args := []string{
			"-re",
			"-stream_loop", "-1",
			"-i", abs,
			"-an",
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-tune", "zerolatency",
			"-loglevel", "panic",
			"-profile:v", prof,
			"-level:v", lvl,
			"-x264-params", "scenecut=0:open_gop=0:repeat-headers=1:force-cfr=1",
			"-bf", "0",
			"-g", "30",
			"-keyint_min", "30",
			"-force_key_frames", "expr:gte(t,n_forced*1)",
			"-f", "h264",
			"-hide_banner",
			"-",
		}
		cmd = exec.Command("ffmpeg", args...)
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

	defer func() {
		if waitErr := cmd.Wait(); waitErr != nil && !errors.Is(waitErr, os.ErrProcessDone) {
			log.Printf("video wait: %v", waitErr)
		}
	}()

	go func() {
		<-ctx.Done()
		_ = cmd.Process.Kill()
	}()

	if codec == "video/VP8" {
		reader, header, err := ivfreader.NewWith(stdout)
		if err != nil {
			log.Printf("ivf open: %v", err)
			_ = cmd.Process.Kill()
			return
		}
		frameDur := time.Duration(float64(time.Second) * float64(header.TimebaseNumerator) / float64(header.TimebaseDenominator))
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			frame, _, err := reader.ParseNextFrame()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				log.Printf("ivf read: %v", err)
				return
			}
			if werr := track.WriteSample(media.Sample{Data: frame, Duration: frameDur}); werr != nil {
				if errors.Is(werr, io.ErrClosedPipe) || errors.Is(werr, webrtc.ErrConnectionClosed) {
					time.Sleep(50 * time.Millisecond)
					continue
				}
				log.Printf("video write: %v", werr)
				return
			}
		}
	} else {
		reader := bufio.NewReader(stdout)
		startCode := []byte{0x00, 0x00, 0x00, 0x01}
		frameDur := 33 * time.Millisecond
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			nal, err := readNextNAL(reader, startCode)
			if len(nal) > 0 {
				payload := append([]byte{}, nal...)
				if !bytes.HasPrefix(payload, startCode) {
					payload = append(startCode, payload...)
				}
				if werr := track.WriteSample(media.Sample{Data: payload, Duration: frameDur}); werr != nil {
					if errors.Is(werr, io.ErrClosedPipe) || errors.Is(werr, webrtc.ErrConnectionClosed) {
						time.Sleep(50 * time.Millisecond)
						continue
					}
					log.Printf("h264 write: %v", werr)
					return
				}
				time.Sleep(frameDur)
			}

			if err == io.EOF {
				return
			}
			if err != nil {
				log.Printf("h264 read: %v", err)
				return
			}
		}
	}
}

func pumpAudio(ctx context.Context, path string, track *webrtc.TrackLocalStaticSample) {
	if track == nil {
		return
	}
	abs, _ := filepath.Abs(path)
	cmd := exec.Command("ffmpeg",
		"-fflags", "+genpts",
		"-use_wallclock_as_timestamps", "1",
		"-re", "-i", abs,
		"-vn",
		"-ac", "2",
		"-ar", "48000",
		"-c:a", "libopus",
		"-b:a", "96k",
		"-application", "lowdelay",
		"-loglevel", "panic",
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

	defer func() {
		if waitErr := cmd.Wait(); waitErr != nil && !errors.Is(waitErr, os.ErrProcessDone) {
			log.Printf("audio wait: %v", waitErr)
		}
	}()

	go func() {
		<-ctx.Done()
		_ = cmd.Process.Kill()
	}()

	ogg, _, err := oggreader.NewWith(stdout)
	if err != nil {
		log.Printf("ogg open: %v", err)
		_ = cmd.Process.Kill()
		return
	}
	frameDur := 20 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		p, _, err := ogg.ParseNextPage()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			log.Printf("ogg read: %v", err)
			return
		}
		if werr := track.WriteSample(media.Sample{Data: p, Duration: frameDur}); werr != nil {
			if errors.Is(werr, io.ErrClosedPipe) || errors.Is(werr, webrtc.ErrConnectionClosed) {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			log.Printf("audio write: %v", werr)
			return
		}
	}
}

type mp4OfferRequest struct {
	SDP string `json:"sdp"`
}

type mp4OfferResponse struct {
	SDP   string `json:"sdp,omitempty"`
	Error string `json:"error,omitempty"`
	Pback *mp4Playback
}

type mp4SelectRequest struct {
	Path string `json:"path"`
}

type mp4SelectResponse struct {
	Current string `json:"current,omitempty"`
	Error   string `json:"error,omitempty"`
}

type mp4ListResponse struct {
	Files   []string `json:"files"`
	Current string   `json:"current,omitempty"`
	Error   string   `json:"error,omitempty"`
}

func processOffer(req mp4OfferRequest) mp4OfferResponse {
	mp4Path := currentMP4ServePath()
	fmt.Printf("\r\n current path: %v", mp4Path)
	if strings.TrimSpace(mp4Path) == "" {
		return mp4OfferResponse{Error: "mp4 test server not configured"}
	}
	if strings.TrimSpace(req.SDP) == "" {
		return mp4OfferResponse{Error: "empty SDP"}
	}

	isvr := Config.GetICEServers()
	var ices []webrtc.ICEServer

	for index, sz := range isvr {
		if strings.HasPrefix(sz, "stun") {
			ices = append(ices, webrtc.ICEServer{URLs: []string{sz}})
		}
		fmt.Printf("\r\nIndex: %d, val: %s", index, sz)
	}

	resp := mp4OfferResponse{}
	if err := func() error {
		m := &webrtc.MediaEngine{}
		if err := m.RegisterDefaultCodecs(); err != nil {
			return fmt.Errorf("media engine: %w", err)
		}
		api := webrtc.NewAPI(webrtc.WithMediaEngine(m))
		pc, err := api.NewPeerConnection(webrtc.Configuration{
			ICEServers: ices,
		})
		if err != nil {
			return fmt.Errorf("peer connection: %w", err)
		}

		videoTrack, err := webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000},
			"mp4video", "mp4test")
		if err != nil {
			pc.Close()
			return fmt.Errorf("video track: %w", err)
		}
		videoSender, err := pc.AddTrack(videoTrack)
		if err != nil {
			pc.Close()
			return fmt.Errorf("add video track: %w", err)
		}
		go drainRTCP(videoSender)

		audioTrack, err := webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
			"mp4audio", "mp4test")
		if err != nil {
			pc.Close()
			return fmt.Errorf("audio track: %w", err)
		}
		audioSender, err := pc.AddTrack(audioTrack)
		if err != nil {
			pc.Close()
			return fmt.Errorf("add audio track: %w", err)
		}
		go drainRTCP(audioSender)

		playback, err := newMP4Playback(mp4Path, webrtc.MimeTypeH264, videoTrack, audioTrack, "", "")
		if err != nil {
			pc.Close()
			return fmt.Errorf("mp4 playback: %w", err)
		}
		resp.Pback = playback

		cleanup := func() {
			playback.Stop()
			_ = pc.Close()
		}

		pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
			fmt.Printf("\r\nmp4 test peer state: %s", state)
			switch state {
			case webrtc.PeerConnectionStateConnected:
				playback.Start()
			case webrtc.PeerConnectionStateFailed,
				webrtc.PeerConnectionStateClosed,
				webrtc.PeerConnectionStateDisconnected:
				cleanup()
			}
		})

		offer := webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer,
			SDP:  req.SDP,
		}
		if err := pc.SetRemoteDescription(offer); err != nil {
			cleanup()
			return fmt.Errorf("set remote description: %w", err)
		}

		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			cleanup()
			return fmt.Errorf("create answer: %w", err)
		}
		if err := pc.SetLocalDescription(answer); err != nil {
			cleanup()
			return fmt.Errorf("set local description: %w", err)
		}

		<-webrtc.GatheringCompletePromise(pc)
		local := pc.CurrentLocalDescription()
		if local == nil {
			cleanup()
			return fmt.Errorf("local description missing")
		}

		resp.SDP = local.SDP
		return nil
	}(); err != nil {
		log.Printf("mp4 test offer error: %v", err)
		resp.Error = err.Error()
	}

	//fmt.Printf("\r\nresponse: %v", resp)

	return resp
}

// StartMP4TestServer launches a lightweight HTTP + WebRTC server that streams the provided MP4 file.
// Visit http://addr/ with a browser and click "Start Playback" to test the recording via WebRTC.
func StartMP4TestServer(addr string, mp4Path string) {
	if err := SwitchMP4PlaybackFile(mp4Path); err != nil {
		log.Printf("mp4 test server: unable to set initial file %q: %v", mp4Path, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		currentPath := currentMP4RelativePath()
		if strings.TrimSpace(currentPath) == "" {
			currentPath = "(file not configured)"
		}
		page := strings.ReplaceAll(mp4TestPageHTML, "{{MP4_PATH}}",
			template.HTMLEscapeString(currentPath))
		fmt.Fprint(w, page)
	})

	mux.HandleFunc("/offer", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("\r\nwe got offer")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()

		var req mp4OfferRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
		resp := processOffer(req)
		if resp.Error != "" {
			if resp.Error == "empty SDP" {
				http.Error(w, resp.Error, http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/mp4/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		files, err := listAvailableMP4Files(recordingsRoot)
		resp := mp4ListResponse{
			Files:   files,
			Current: currentMP4RelativePath(),
		}
		if err != nil {
			resp.Error = err.Error()
			w.WriteHeader(http.StatusInternalServerError)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/mp4/select", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()

		var req mp4SelectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
		abs, err := resolveRecordingPath(req.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := SwitchMP4PlaybackFile(abs); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp := mp4SelectResponse{Current: currentMP4RelativePath()}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/mp4/current", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp := mp4SelectResponse{Current: currentMP4RelativePath()}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/mp4/file", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := r.URL.Query().Get("path")
		if strings.TrimSpace(path) == "" {
			http.Error(w, "missing path", http.StatusBadRequest)
			return
		}
		abs, err := resolveRecordingPath(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.ServeFile(w, r, abs)
	})

	go func() {
		log.Printf("MP4 test server listening on %s (file: %s)", addr, currentMP4ServePath())
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("mp4 test server terminated: %v", err)
		}
	}()
}
