package statistics

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"go.uber.org/atomic"

	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/neilalexander/harmony/federationapi/storage"
)

// Statistics contains information about all of the remote federated
// hosts that we have interacted with. It is basically a threadsafe
// wrapper.
type Statistics struct {
	DB      storage.Database
	servers map[spec.ServerName]*ServerStatistics
	mutex   sync.RWMutex

	backoffTimers map[spec.ServerName]*time.Timer
	backoffMutex  sync.RWMutex

	// How many times should we tolerate consecutive failures before we
	// just blacklist the host altogether? The backoff is exponential,
	// so the max time here to attempt is 2**failures seconds.
	FailuresUntilBlacklist uint32
}

func NewStatistics(
	db storage.Database,
	failuresUntilBlacklist uint32,
) Statistics {
	return Statistics{
		DB:                     db,
		FailuresUntilBlacklist: failuresUntilBlacklist,
		backoffTimers:          make(map[spec.ServerName]*time.Timer),
		servers:                make(map[spec.ServerName]*ServerStatistics),
	}
}

// ForServer returns server statistics for the given server name. If it
// does not exist, it will create empty statistics and return those.
func (s *Statistics) ForServer(serverName spec.ServerName) *ServerStatistics {
	// Look up if we have statistics for this server already.
	s.mutex.RLock()
	server, found := s.servers[serverName]
	s.mutex.RUnlock()
	// If we don't, then make one.
	if !found {
		s.mutex.Lock()
		server = &ServerStatistics{
			statistics: s,
			serverName: serverName,
		}
		s.servers[serverName] = server
		s.mutex.Unlock()
		blacklisted, err := s.DB.IsServerBlacklisted(serverName)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to get blacklist entry %q", serverName)
		} else {
			server.blacklisted.Store(blacklisted)
		}
	}
	return server
}

// ServerStatistics contains information about our interactions with a
// remote federated host, e.g. how many times we were successful, how
// many times we failed etc. It also manages the backoff time and black-
// listing a remote host if it remains uncooperative.
type ServerStatistics struct {
	statistics      *Statistics     //
	serverName      spec.ServerName //
	blacklisted     atomic.Bool     // is the node blacklisted
	backoffStarted  atomic.Bool     // is the backoff started
	backoffUntil    atomic.Value    // time.Time until this backoff interval ends
	backoffCount    atomic.Uint32   // number of times BackoffDuration has been called
	successCounter  atomic.Uint32   // how many times have we succeeded?
	backoffNotifier func()          // notifies destination queue when backoff completes
	notifierMutex   sync.Mutex
}

const maxJitterMultiplier = 1.4
const minJitterMultiplier = 0.8

// duration returns how long the next backoff interval should be.
func (s *ServerStatistics) duration(count uint32) time.Duration {
	// Add some jitter to minimise the chance of having multiple backoffs
	// ending at the same time.
	jitter := rand.Float64()*(maxJitterMultiplier-minJitterMultiplier) + minJitterMultiplier
	duration := time.Millisecond * time.Duration(math.Exp2(float64(count))*jitter*1000)
	return duration
}

// cancel will interrupt the currently active backoff.
func (s *ServerStatistics) cancel() {
	s.blacklisted.Store(false)
	s.backoffUntil.Store(time.Time{})

	s.ClearBackoff()
}

// AssignBackoffNotifier configures the channel to send to when
// a backoff completes.
func (s *ServerStatistics) AssignBackoffNotifier(notifier func()) {
	s.notifierMutex.Lock()
	defer s.notifierMutex.Unlock()
	s.backoffNotifier = notifier
}

// Success updates the server statistics with a new successful
// attempt, which increases the sent counter and resets the idle and
// failure counters. If a host was blacklisted at this point then
// we will unblacklist it.
// `relay` specifies whether the success was to the actual destination
// or one of their relay servers.
func (s *ServerStatistics) Success() {
	s.cancel()
	s.backoffCount.Store(0)
}

// Failure marks a failure and starts backing off if needed.
// It will return the time that the current failure
// will result in backoff waiting until, and a bool signalling
// whether we have blacklisted and therefore to give up.
func (s *ServerStatistics) Failure() (time.Time, bool) {
	// Return immediately if we have blacklisted this node.
	if s.blacklisted.Load() {
		return time.Time{}, true
	}

	// If we aren't already backing off, this call will start
	// a new backoff period, increase the failure counter and
	// start a goroutine which will wait out the backoff and
	// unset the backoffStarted flag when done.
	if s.backoffStarted.CompareAndSwap(false, true) {
		backoffCount := s.backoffCount.Inc()

		if backoffCount >= s.statistics.FailuresUntilBlacklist {
			s.blacklisted.Store(true)
			if s.statistics.DB != nil {
				if err := s.statistics.DB.AddServerToBlacklist(s.serverName); err != nil {
					logrus.WithError(err).Errorf("Failed to add %q to blacklist", s.serverName)
				}
			}
			s.ClearBackoff()
			return time.Time{}, true
		}

		// We're starting a new back off so work out what the next interval
		// will be.
		count := s.backoffCount.Load()
		until := time.Now().Add(s.duration(count))
		s.backoffUntil.Store(until)

		s.statistics.backoffMutex.Lock()
		s.statistics.backoffTimers[s.serverName] = time.AfterFunc(time.Until(until), s.backoffFinished)
		s.statistics.backoffMutex.Unlock()
	}

	return s.backoffUntil.Load().(time.Time), false
}

// MarkServerAlive removes the assumed offline and blacklisted statuses from this server.
// Returns whether the server was blacklisted before this point.
func (s *ServerStatistics) MarkServerAlive() bool {
	return s.removeBlacklist()
}

// ClearBackoff stops the backoff timer for this destination if it is running
// and removes the timer from the backoffTimers map.
func (s *ServerStatistics) ClearBackoff() {
	// If the timer is still running then stop it so it's memory is cleaned up sooner.
	s.statistics.backoffMutex.Lock()
	defer s.statistics.backoffMutex.Unlock()
	if timer, ok := s.statistics.backoffTimers[s.serverName]; ok {
		timer.Stop()
	}
	delete(s.statistics.backoffTimers, s.serverName)

	s.backoffStarted.Store(false)
}

// backoffFinished will clear the previous backoff and notify the destination queue.
func (s *ServerStatistics) backoffFinished() {
	s.ClearBackoff()

	// Notify the destinationQueue if one is currently running.
	s.notifierMutex.Lock()
	defer s.notifierMutex.Unlock()
	if s.backoffNotifier != nil {
		s.backoffNotifier()
	}
}

// BackoffInfo returns information about the current or previous backoff.
// Returns the last backoffUntil time.
func (s *ServerStatistics) BackoffInfo() *time.Time {
	until, ok := s.backoffUntil.Load().(time.Time)
	if ok {
		return &until
	}
	return nil
}

// Blacklisted returns true if the server is blacklisted and false
// otherwise.
func (s *ServerStatistics) Blacklisted() bool {
	return s.blacklisted.Load()
}

// removeBlacklist removes the blacklisted status from the server.
// Returns whether the server was blacklisted.
func (s *ServerStatistics) removeBlacklist() bool {
	var wasBlacklisted bool

	if s.Blacklisted() {
		wasBlacklisted = true
		_ = s.statistics.DB.RemoveServerFromBlacklist(s.serverName)
	}
	s.cancel()
	s.backoffCount.Store(0)

	return wasBlacklisted
}

// SuccessCount returns the number of successful requests. This is
// usually useful in constructing transaction IDs.
func (s *ServerStatistics) SuccessCount() uint32 {
	return s.successCounter.Load()
}
