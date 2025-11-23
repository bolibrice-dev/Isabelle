package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"log"
	"time"

	"github.com/deepch/vdk/av"
	"github.com/deepch/vdk/codec/h264parser"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

var (
	ErrorNotFound          = errors.New("WebRTC Stream Not Found")
	ErrorCodecNotSupported = errors.New("WebRTC Codec Not Supported")
	ErrorClientOffline     = errors.New("WebRTC Client Offline")
	ErrorNotTrackAvailable = errors.New("WebRTC No Track Available")
	ErrorIgnoreAudioTrack  = errors.New("WebRTC Ignore Audio Track codec not supported WebRTC support only PCM_ALAW or PCM_MULAW")
)

type Moxer struct {
	streams     map[int8]*myStream
	status      webrtc.ICEConnectionState
	stop        bool
	pc          *webrtc.PeerConnection
	ClientACK   *time.Timer
	StreamACK   *time.Timer
	Options     myOptions
	current_cam string
	mp4         string
	avTrack     *webrtc.TrackLocalStaticSample
	auTrack     *webrtc.TrackLocalStaticSample

	UUID0 string
	UUID1 string
}

type myStream struct {
	codec av.CodecData
	track *webrtc.TrackLocalStaticSample
}
type myOptions struct {
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

func NewMoxer(options myOptions) *Moxer {
	tmp := Moxer{Options: options, ClientACK: time.NewTimer(time.Second * 20), StreamACK: time.NewTimer(time.Second * 20), streams: make(map[int8]*myStream)}
	//go tmp.WaitCloser()

	return &tmp
}

func (element *Moxer) Set_UUID0(uuid0 string) {
	element.UUID0 = uuid0
}

func (element *Moxer) Set_UUID1(uuid1 string) {
	element.UUID1 = uuid1
}

func (element *Moxer) OnICECandidate(c *webrtc.ICECandidateInit) error {
	if c != nil {
		if element.pc != nil {
			element.pc.AddICECandidate(*c)
		}
	}

	return nil
}
func (element *Moxer) GetPeerConnection() *webrtc.PeerConnection {
	return element.pc
}
func (element *Moxer) GetVideoTrack() (t *webrtc.TrackLocalStaticSample) {
	t = element.avTrack
	return t
}

func (element *Moxer) GetAudioTrack() (t *webrtc.TrackLocalStaticSample) {
	t = element.auTrack
	return t
}

func (element *Moxer) GetStreams() map[int8]*myStream {
	s := element.streams
	//log.Println(s)
	return s
}

func (element *Moxer) NewPeerConnection(configuration webrtc.Configuration) (*webrtc.PeerConnection, error) {
	if len(element.Options.ICEServers) > 0 {
		// log.Println("Set ICEServers", element.Options.ICEServers)
		configuration.ICEServers = append(configuration.ICEServers, webrtc.ICEServer{
			URLs:           element.Options.ICEServers,
			Username:       element.Options.ICEUsername,
			Credential:     element.Options.ICECredential,
			CredentialType: webrtc.ICECredentialTypePassword,
		})
	} else {
		configuration.ICEServers = append(configuration.ICEServers, webrtc.ICEServer{
			URLs: []string{"stun:stun.l.google.com:19302"},
		})
	}
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	i := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, i); err != nil {
		return nil, err
	}
	s := webrtc.SettingEngine{}
	if element.Options.PortMin > 0 && element.Options.PortMax > 0 && element.Options.PortMax > element.Options.PortMin {
		s.SetEphemeralUDPPortRange(element.Options.PortMin, element.Options.PortMax)
		log.Println("Set UDP ports to", element.Options.PortMin, "..", element.Options.PortMax)
	}
	if len(element.Options.ICECandidates) > 0 {
		s.SetNAT1To1IPs(element.Options.ICECandidates, webrtc.ICECandidateTypeHost)
		log.Println("Set ICECandidates", element.Options.ICECandidates)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i), webrtc.WithSettingEngine(s))
	return api.NewPeerConnection(configuration)
}

func (element *Moxer) CreatePC() *webrtc.PeerConnection {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.cloudflare.com:3478"},
			},
		},
	}
	// Create a new PeerConnection
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}
	return pc
}

func (element *Moxer) WriteCloudfareHeader(streams []av.CodecData,
	setpc func(*webrtc.PeerConnection)) error {
	//log.Println(len(element.streams))
	setpc(element.CreatePC())
	return nil
}

func (element *Moxer) SetStatus(connState webrtc.ICEConnectionState) {
	element.status = connState
}

func (element *Moxer) TransmitActualTracks(streams []av.CodecData,
	peerConnection *webrtc.PeerConnection) (string, error) {
	for i, i2 := range streams {
		var track *webrtc.TrackLocalStaticSample
		var err error
		if i2.Type().IsVideo() {
			if i2.Type() == av.H264 {
				//unique_id := element.pseudo_uuid()
				track, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
					MimeType: webrtc.MimeTypeH264,
				}, element.UUID0, "cashan-video")
				if err != nil {
					return "", err
				}
				if rtpSender, err := peerConnection.AddTrack(track); err != nil {
					return "", err
				} else {
					element.avTrack = track
					go func() {
						rtcpBuf := make([]byte, 1500)
						for {
							if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
								return
							}
						}
					}()
				}
			}
		} else if i2.Type().IsAudio() {

			AudioCodecString := webrtc.MimeTypePCMA
			switch i2.Type() {
			case av.PCM_ALAW:
				AudioCodecString = webrtc.MimeTypePCMA
			case av.PCM_MULAW:
				AudioCodecString = webrtc.MimeTypePCMU
			case av.OPUS:
				AudioCodecString = webrtc.MimeTypeOpus
			default:
				log.Println(ErrorIgnoreAudioTrack)
				continue
			}

			track, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
				MimeType:  AudioCodecString,
				Channels:  uint16(i2.(av.AudioCodecData).ChannelLayout().Count()),
				ClockRate: uint32(i2.(av.AudioCodecData).SampleRate()),
			}, element.UUID1, "cashan-rtsp-audio")
			if err != nil {
				return "", err
			}
			if rtpSender, err := peerConnection.AddTrack(track); err != nil {
				return "", err
			} else {
				element.auTrack = track

				//log.Println("Processing audio track w/ codec", AudioCodecString)
				go func() {
					rtcpBuf := make([]byte, 1500)
					for {
						if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
							return
						}
					}
				}()
			}
		}
		element.streams[int8(i)] = &myStream{track: track, codec: i2}
	}
	if len(element.streams) == 0 {
		return "", ErrorNotTrackAvailable
	}
	return "", nil
}

// following function contains reference to the main PeerConnection for current connection
func (element *Moxer) WriteHeader(streams []av.CodecData, sdp64 string,
	mp4file string,
	callback func(*webrtc.ICECandidate), callback2 func(webrtc.SessionDescription),
	setpc func(*webrtc.PeerConnection, *Moxer, string, string, string),
	requestor string, suuid string) (string, error) {
	// sdp64 string,
	//var sdp64 string = ctx.PostForm("data")
	var WriteHeaderSuccess bool
	element.mp4 = "none"
	if len(streams) == 0 {
		return "", ErrorNotFound
	}
	sdpB, err := base64.StdEncoding.DecodeString(sdp64)
	if err != nil {
		return "", err
	}
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(sdpB),
	}
	peerConnection, err := element.NewPeerConnection(webrtc.Configuration{
		SDPSemantics: webrtc.SDPSemanticsUnifiedPlanWithFallback,
	})
	if err != nil {
		return "", err
	}

	setpc(peerConnection, element, mp4file, requestor, suuid)

	defer func() {
		if !WriteHeaderSuccess {
			err = element.Close()
			if err != nil {
				log.Println(err)
			}
		}
	}()
	element.TransmitActualTracks(streams, peerConnection)
	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			callback(c)
		}
	})

	if err = peerConnection.SetRemoteDescription(offer); err != nil {
		return "", err
	}

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		return "", err
	}
	callback2(answer)
	//log.Println("cs answer", answer)
	gatherCompletePromise := webrtc.GatheringCompletePromise(peerConnection)

	if err = peerConnection.SetLocalDescription(answer); err != nil {
		return "", err
	}
	element.pc = peerConnection
	waitT := time.NewTimer(time.Second * 8)
	select {
	case <-waitT.C:
		return "", errors.New("gatherCompletePromise wait")
	case <-gatherCompletePromise:
		//Connected
		//log.Println("Done gathering candis")
	}
	resp := peerConnection.LocalDescription()
	WriteHeaderSuccess = true
	//log.Println("exiting function WriteHeader( )")
	return base64.StdEncoding.EncodeToString([]byte(resp.SDP)), nil

}

func (element *Moxer) GetCurrentCam() (c string) {
	c = element.current_cam
	return c
}

func (element *Moxer) GetMP4() (c string) {
	return element.mp4
}
func (element *Moxer) SetMP4(c string) {
	element.mp4 = c
}

func (element *Moxer) SetCurrentCam(c string) {
	element.current_cam = c
}

func (element *Moxer) WritePacket(pkt av.Packet) (err error) {
	//log.Println("WritePacket", pkt.Time, element.stop, webrtc.ICEConnectionStateConnected, pkt.Idx, element.streams[pkt.Idx])
	var WritePacketSuccess bool
	//log.Println("inside WritePacket")
	defer func() {
		if !WritePacketSuccess {
			//log.Println("couldn't write packet")
			element.Close()
		}
	}()
	if element.stop {
		//log.Println("error: client is offline??")
		return ErrorClientOffline
	}
	if inlinePlaybackActive(element) {
		WritePacketSuccess = true
		return nil
	}
	if element.status == webrtc.ICEConnectionStateChecking {
		WritePacketSuccess = true
		//log.Println("success: connection is checking??")
		return nil
	}
	if element.status != webrtc.ICEConnectionStateConnected {
		//log.Println("**Hold on** we are not yet connected")
		return nil
	}
	if tmp, ok := element.streams[pkt.Idx]; ok {
		element.StreamACK.Reset(10 * time.Second)
		if len(pkt.Data) < 5 {
			return nil
		}
		switch tmp.codec.Type() {
		case av.H264:
			nalus, _ := h264parser.SplitNALUs(pkt.Data)
			for _, nalu := range nalus {
				naltype := nalu[0] & 0x1f
				if naltype == 5 {
					codec := tmp.codec.(h264parser.CodecData)
					err = tmp.track.WriteSample(media.Sample{Data: append([]byte{0, 0, 0, 1}, bytes.Join([][]byte{codec.SPS(), codec.PPS(), nalu}, []byte{0, 0, 0, 1})...), Duration: pkt.Duration})
				} else {
					err = tmp.track.WriteSample(media.Sample{Data: append([]byte{0, 0, 0, 1}, nalu...), Duration: pkt.Duration})
				}
				if err != nil {
					return err
				}

			}
			WritePacketSuccess = true
			return

		case av.PCM_ALAW:
		case av.OPUS:
		case av.PCM_MULAW:
		case av.AAC:
			//TODO: NEED ADD DECODER AND ENCODER
			return ErrorCodecNotSupported
		case av.PCM:
			//TODO: NEED ADD ENCODER
			return ErrorCodecNotSupported
		default:
			return ErrorCodecNotSupported
		}
		err = tmp.track.WriteSample(media.Sample{Data: pkt.Data, Duration: pkt.Duration})
		if err == nil {
			WritePacketSuccess = true
		}
		return err
	} else {
		WritePacketSuccess = true
		return nil
	}
}

func (element *Moxer) Close() error {
	element.stop = true
	if element.pc != nil {
		err := element.pc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
