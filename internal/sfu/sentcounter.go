package sfu

import (
	"strings"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"

	"github.com/ssubbotin/kunveno/internal/metrics"
)

// sentCounter counts every RTP packet the server sends, per subscriber connection, so the
// outgoing load (one speaker fanned out to everyone else) is visible in metrics.
type sentCounter struct{ interceptor.NoOp }

type sentCounterFactory struct{}

func (sentCounterFactory) NewInterceptor(string) (interceptor.Interceptor, error) {
	return &sentCounter{}, nil
}

func (*sentCounter) BindLocalStream(info *interceptor.StreamInfo, writer interceptor.RTPWriter) interceptor.RTPWriter {
	kind := KindAudio.String()
	if strings.HasPrefix(strings.ToLower(info.MimeType), "video/") {
		kind = KindScreen.String()
	}
	packets := metrics.RTPSentPackets.WithLabelValues(kind)
	bytes := metrics.RTPSentBytes.WithLabelValues(kind)
	return interceptor.RTPWriterFunc(func(h *rtp.Header, payload []byte, a interceptor.Attributes) (int, error) {
		n, err := writer.Write(h, payload, a)
		if err == nil {
			packets.Inc()
			bytes.Add(float64(len(payload)))
		}
		return n, err
	})
}
