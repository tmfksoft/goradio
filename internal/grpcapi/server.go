// Package grpcapi implements the AudioServerService gRPC API.
package grpcapi

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	audioserverv1 "github.com/tmfksoft/goradio/gen/go/audioserver/v1"
	"github.com/tmfksoft/goradio/internal/auth"
	"github.com/tmfksoft/goradio/internal/playback"
	"github.com/tmfksoft/goradio/internal/registry"
)

// Prefetcher dispatches a queued item's download/transcode job. Handed in
// as an interface so grpcapi doesn't depend on the transcode package's
// concrete config/cache types.
type Prefetcher interface {
	Prefetch(item *playback.QueuedItem)
}

// StationStarter is called once, the first time a station is registered,
// so the caller (cmd/radio's serve wiring) can start its player goroutine.
type StationStarter interface {
	StartStation(st *playback.Station)
}

type Server struct {
	audioserverv1.UnimplementedAudioServerServiceServer

	log           *slog.Logger
	registry      *registry.Registry
	prefetcher    Prefetcher
	starter       StationStarter
	publicBaseURL string
}

func NewServer(log *slog.Logger, reg *registry.Registry, prefetcher Prefetcher, starter StationStarter, publicBaseURL string) *Server {
	return &Server{
		log:           log,
		registry:      reg,
		prefetcher:    prefetcher,
		starter:       starter,
		publicBaseURL: publicBaseURL,
	}
}

func (s *Server) RegisterStation(ctx context.Context, req *audioserverv1.RegisterStationRequest) (*audioserverv1.RegisterStationResponse, error) {
	if req.GetSlug() == "" {
		return nil, status.Error(codes.InvalidArgument, "slug is required")
	}
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}

	st, reRegistered := s.registry.Register(req.GetSlug(), req.GetName(), req.GetDescription(), func(newStation *playback.Station) {
		if s.starter != nil {
			s.starter.StartStation(newStation)
		}
	})

	s.log.Info("station registered", "slug", st.Slug, "name", st.Name(), "re_registered", reRegistered)

	return &audioserverv1.RegisterStationResponse{
		Slug:         st.Slug,
		StreamUrl:    fmt.Sprintf("%s/stream/%s", s.publicBaseURL, st.Slug),
		ReRegistered: reRegistered,
	}, nil
}

func (s *Server) QueueTrack(ctx context.Context, req *audioserverv1.QueueTrackRequest) (*audioserverv1.QueueTrackResponse, error) {
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}
	if req.GetSource() == nil {
		return nil, status.Error(codes.InvalidArgument, "source is required")
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
	}

	transition := req.GetTransition()
	if transition == audioserverv1.Transition_TRANSITION_CROSSFADE {
		s.log.Warn("crossfade transition requested but not implemented this phase; coercing to hard cut", "slug", req.GetSlug())
		transition = audioserverv1.Transition_TRANSITION_HARD_CUT
	}

	item := playback.NewQueuedItem(uuid.NewString(), req.GetSource(), req.GetMode(), transition)
	if s.prefetcher != nil {
		s.prefetcher.Prefetch(item)
	} else {
		item.MarkReady("", fmt.Errorf("no prefetcher configured"))
	}

	var position int
	switch req.GetMode() {
	case audioserverv1.QueueMode_QUEUE_MODE_PLAY_NEXT, audioserverv1.QueueMode_QUEUE_MODE_PLAY_NOW_INTERRUPT:
		st.Queue.PushFront(item)
		position = 0
		if req.GetMode() == audioserverv1.QueueMode_QUEUE_MODE_PLAY_NOW_INTERRUPT {
			st.Interrupt()
		}
	default:
		position = st.Queue.Append(item)
	}

	st.PublishQueueUpdated()

	return &audioserverv1.QueueTrackResponse{
		QueueId:       item.ID,
		QueuePosition: int32(position),
		Status:        "queued",
	}, nil
}

func (s *Server) GetStatus(ctx context.Context, req *audioserverv1.GetStatusRequest) (*audioserverv1.GetStatusResponse, error) {
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return nil, err
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return &audioserverv1.GetStatusResponse{Slug: req.GetSlug(), IsRegistered: false}, nil
	}

	resp := &audioserverv1.GetStatusResponse{
		Slug:          st.Slug,
		Name:          st.Name(),
		IsRegistered:  true,
		IsSilence:     st.IsSilence(),
		ListenerCount: int64(st.Broadcaster.ListenerCount()),
		UptimeSeconds: int64(st.Uptime().Seconds()),
	}

	if cur := st.Current(); cur != nil {
		resp.CurrentTrack = queuedItemToStatus(cur)
	}

	for _, item := range st.Queue.Snapshot() {
		resp.Queue = append(resp.Queue, queuedItemToStatus(item))
	}

	return resp, nil
}

func (s *Server) SubscribeEvents(req *audioserverv1.SubscribeEventsRequest, stream audioserverv1.AudioServerService_SubscribeEventsServer) error {
	ctx := stream.Context()
	if err := auth.RequireSlug(ctx, req.GetSlug()); err != nil {
		return err
	}

	st, ok := s.registry.Get(req.GetSlug())
	if !ok {
		return status.Errorf(codes.NotFound, "station %q is not registered", req.GetSlug())
	}

	_, ch, unsubscribe := st.Events.Subscribe()
	defer unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}

func queuedItemToStatus(item *playback.QueuedItem) *audioserverv1.QueuedItemStatus {
	return &audioserverv1.QueuedItemStatus{
		QueueId: item.ID,
		Source:  item.Source,
		Mode:    item.Mode,
	}
}
