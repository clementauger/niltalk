package notify

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"time"

	rice "github.com/GeertJohan/go.rice"
	"github.com/faiface/beep"
	"github.com/faiface/beep/flac"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
	"github.com/faiface/beep/wav"
	"github.com/gen2brain/beeep"
	tparse "github.com/karrick/tparse/v2"
	"golang.org/x/time/rate"
)

type Notifier struct {
	BaseURL     string
	RoomID      string
	Logger      *log.Logger
	Options     Options
	tpl         *template.Template
	limiter     *rate.Limiter
	soundBuffer *beep.Buffer
	box         *rice.Box
}

type Options struct {
	Icon            string `koanf:"icon"`
	Enabler         string `koanf:"enabler"`
	Message         string `koanf:"message"`
	Title           string `koanf:"title"`
	Sound           string `koanf:"sound"`
	RateLimitPeriod string `koanf:"rate-limit-period"`
	RateLimitCount  string `koanf:"rate-limit-count"`
	RateLimitBurst  string `koanf:"rate-limit-burst"`
}

func New(opt Options, baseURL, roomID string, logger *log.Logger, box *rice.Box) *Notifier {
	return &Notifier{
		Options: opt,
		BaseURL: baseURL,
		RoomID:  roomID,
		Logger:  logger,
		box:     box,
	}
}

func (n *Notifier) Init() error {
	{
		t, err := template.New("").Parse(n.Options.Message)
		if err != nil {
			n.Logger.Printf("error compiling growl template for room %q: %v", n.RoomID, err)
			return err
		}
		n.tpl = t
	}

	if n.Options.Sound != "" {
		var r io.ReadCloser
		var err error
		if strings.HasPrefix(n.Options.Sound, "http://") ||
			strings.HasPrefix(n.Options.Sound, "https://") {
			var resp *http.Response
			resp, err = http.Get(n.Options.Sound)
			if err == nil {
				z := new(bytes.Buffer)
				_, err = io.Copy(z, resp.Body)
				resp.Body.Close()
				r = ioutil.NopCloser(z)
			}
		} else {
			r, err = n.box.Open(n.Options.Sound)
		}
		if err != nil {
			n.Logger.Printf("error loading growl sound for room %q: %v", n.RoomID, err)
			return err
		}
		var (
			streamer beep.StreamSeekCloser
			format   beep.Format
		)
		switch filepath.Ext(n.Options.Sound) {
		case ".mp3":
			streamer, format, err = mp3.Decode(r)
		case ".wav":
			streamer, format, err = wav.Decode(r)
		case ".flac":
			streamer, format, err = flac.Decode(r)
		}
		if err != nil {
			n.Logger.Printf("error loading growl sound for room %q: %v", n.RoomID, err)
			return err
		}
		err = speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))
		if err != nil {
			n.Logger.Printf("error initializing sound system for room %q: %v", n.RoomID, err)
			return err
		}
		buffer := beep.NewBuffer(format)
		buffer.Append(streamer)
		streamer.Close()
		n.soundBuffer = buffer
	}

	{
		var rlPeriod time.Duration = time.Minute * 2
		if n.Options.RateLimitPeriod != "" {
			x, err := tparse.AbsoluteDuration(time.Now(), n.Options.RateLimitPeriod)
			if err != nil {
				n.Logger.Fatalf("error unmarshalling 'growl.rate-limit-period' config: %v", err)
				return err
			}
			rlPeriod = x
		}

		rlCount := 3.0
		if n.Options.RateLimitCount != "" {
			x, err := strconv.ParseFloat(n.Options.RateLimitCount, 64)
			if err != nil {
				n.Logger.Fatalf("error unmarshalling 'growl.rate-limit-count' config: %v", err)
				return err
			}
			rlCount = x
		}

		rlBurst := 3
		if n.Options.RateLimitBurst != "" {
			x, err := strconv.Atoi(n.Options.RateLimitBurst)
			if err != nil {
				n.Logger.Fatalf("error unmarshalling 'growl.rate-limit-burst' config: %v", err)
				return err
			}
			rlBurst = x
		}

		n.limiter = rate.NewLimiter(rate.Every(rlPeriod/time.Duration(rlCount)), rlBurst)
	}

	return nil
}

// OnGrowlMessage handles growl notifications.
func (n *Notifier) OnGrowlMessage(msg, handle, token string) {
	if n.limiter != nil && !n.limiter.Allow() {
		return
	}
	body := n.Options.Message
	var s bytes.Buffer
	u := fmt.Sprintf("%v/r/%v", n.BaseURL, n.RoomID)
	if len(token) > 0 {
		u = fmt.Sprintf("%v/r/%v?al=%v", n.BaseURL, n.RoomID, token)
	}
	err := n.tpl.Execute(&s, map[string]interface{}{
		"URL":      u,
		"UserName": handle,
	})
	if err != nil {
		n.Logger.Printf("error executing growl template for room %q: %v", n.RoomID, err)
	} else {
		body = s.String()
	}
	err = beeep.Notify(n.Options.Title, body, "")
	if err != nil {
		n.Logger.Printf("error sending notification for room %q: %v", n.RoomID, err)
	}
	speaker.Play(n.soundBuffer.Streamer(0, n.soundBuffer.Len()))
}
