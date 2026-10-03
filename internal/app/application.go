package app

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/adminusers"
	"github.com/waiting-here/NonbiriAPI/internal/announcements"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charity"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/checkin"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	gamehost "github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/issues"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/logapi"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/reports"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/resourcebridge"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/stewardautomation"
)

type application struct {
	handler         http.Handler
	authRuntime     *auth.Runtime
	bridge          *resourcebridge.Runtime
	claims          *claim.Service
	resourceRepo    *resources.Repository
	automation      *stewardautomation.Service
	discoveryWorker *resources.DiscoveryWorkerPool
	donations       *donation.Service
	charity         *charity.Service
	charityRouting  *charityrouting.Service
	adaptations     *requestadaptation.Store
	checkin         *checkin.Service
	homeGames       *gamehost.Service
	announcements   *announcements.Service
	issues          *issues.Service
	reports         *reports.Repository
	activities      *activities.Service
	activityRepo    *activities.Repository
	activityEvents  *accountstream.Hub
	adminConfig     *adminapi.SiteConfigRuntime
	adminAlerts     *adminalerts.Repository
	adminUsers      *adminusers.Service
	lifecycle       *lifecycle.Coordinator
	lifecycleCancel context.CancelFunc
	lifecycleDone   <-chan struct{}
	rankingCancel   context.CancelFunc
	rankingDone     <-chan struct{}
	debug           *debug.Hub
	logs            *logapi.Repository
	accountEvents   *accountEventConnections
	forward         *publicForwardRuntime
	games           *gameRuntimeBundle
	audits          *auditRuntime
	activityRuntime *activityRuntime
	failures        <-chan error
	authorizer      *authz.Authorizer
	elevation       *elevation.Manager
	gate            *maintenance.Gate
	registry        *maintenance.Registry
	maintenance     *maintenance.Service
	egress          *egress.Stack

	readiness        *readinessState
	workerCancel     context.CancelFunc
	workerCancelOnce sync.Once
	shutdownDone     chan struct{}
	closeDone        chan struct{}
	closeMu          sync.Mutex
	closePhase       string
	shutdownOnce     sync.Once
	closeOnce        sync.Once
	closeErr         error
}

const (
	discoveryWorkerMaxConcurrent = 4
	discoveryWorkerMaxAdmitted   = 32
	discoveryWorkerTimeout       = 5 * time.Minute
)
