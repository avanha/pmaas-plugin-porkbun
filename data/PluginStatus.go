package data

import "time"

type PluginStatus struct {
	CurrentQueueSize       int
	PeakQueueSize          int
	PeakQueueSizeTime      time.Time
	CurrentRetryQueueSize  int
	PeakRetryQueueSize     int
	PeakRetryQueueSizeTime time.Time
	PeakFailedAttempts     int
	PeakFailedAttemptsTime time.Time
	TotalSuccessCount      int
	TotalErrorCount        int
	LastErrorMessage       string
	LastErrorTime          time.Time
}
