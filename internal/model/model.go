package model

import "time"

// RequestContext is the normalized input used by the policy engine.
type RequestContext struct {
	RequestID     string            `json:"request_id"`
	OrgID         string            `json:"org_id"`
	TenantID      string            `json:"tenant_id"`
	Application   string            `json:"application"`
	Service       string            `json:"service"`
	Environment   string            `json:"environment"`
	SignalType    string            `json:"signal_type"`
	Operation     string            `json:"operation"`
	Endpoint      string            `json:"endpoint"`
	Method        string            `json:"method"`
	UserID        string            `json:"user_id"`
	APIKey        string            `json:"api_key"`
	ClientID      string            `json:"client_id"`
	SourceIP      string            `json:"source_ip"`
	Region        string            `json:"region"`
	Resource      string            `json:"resource"`
	Severity      string            `json:"severity"`
	SpanName      string            `json:"span_name"`
	Topic         string            `json:"topic"`
	ConsumerGroup string            `json:"consumer_group"`
	JobType       string            `json:"job_type"`
	Quantity      int64             `json:"quantity"`
	Priority      string            `json:"priority"`
	Timestamp     time.Time         `json:"timestamp"`
	Tags          map[string]string `json:"tags"`
	Attributes    map[string]string `json:"attributes"`
}

// ActionType describes what the caller should do with the request.
type ActionType string

// Supported action values returned by policy evaluation.
const (
	ActionAllow           ActionType = "allow"
	ActionDeny            ActionType = "deny"
	ActionDelay           ActionType = "delay"
	ActionSample          ActionType = "sample"
	ActionDrop            ActionType = "drop"
	ActionDowngrade       ActionType = "downgrade"
	ActionDropLowPriority ActionType = "drop_low_priority"
	ActionShadowOnly      ActionType = "shadow_only"
)

type Decision struct {
	Allowed         bool              `json:"allowed"`
	Action          ActionType        `json:"action"`
	Reason          string            `json:"reason"`
	MatchedPolicyID string            `json:"matched_policy_id"`
	MatchedRuleKey  string            `json:"matched_rule_key"`
	LimitKey        string            `json:"limit_key"`
	Remaining       int64             `json:"remaining"`
	RetryAfter      time.Duration     `json:"retry_after"`
	ResetAt         time.Time         `json:"reset_at"`
	DelayFor        time.Duration     `json:"delay_for"`
	SampleRate      float64           `json:"sample_rate"`
	ShadowMode      bool              `json:"shadow_mode"`
	Metadata        map[string]string `json:"metadata"`
}

// PolicyScope defines dimensions used to match a request to a policy.
type PolicyScope struct {
	OrgID         string            `json:"org_id"`
	TenantID      string            `json:"tenant_id"`
	Application   string            `json:"application"`
	Service       string            `json:"service"`
	Environment   string            `json:"environment"`
	SignalType    string            `json:"signal_type"`
	Operation     string            `json:"operation"`
	Endpoint      string            `json:"endpoint"`
	Method        string            `json:"method"`
	UserID        string            `json:"user_id"`
	APIKey        string            `json:"api_key"`
	ClientID      string            `json:"client_id"`
	SourceIP      string            `json:"source_ip"`
	Region        string            `json:"region"`
	Resource      string            `json:"resource"`
	Severity      string            `json:"severity"`
	SpanName      string            `json:"span_name"`
	Topic         string            `json:"topic"`
	ConsumerGroup string            `json:"consumer_group"`
	JobType       string            `json:"job_type"`
	Tags          map[string]string `json:"tags"`
}

// AlgorithmType identifies the algorithm selected for a policy.
type AlgorithmType string

// Supported algorithm identifiers.
const (
	AlgoFixedWindow      AlgorithmType = "fixed_window"
	AlgoSlidingWindowLog AlgorithmType = "sliding_window_log"
	AlgoSlidingWindowCnt AlgorithmType = "sliding_window_counter"
	AlgoTokenBucket      AlgorithmType = "token_bucket"
	AlgoLeakyBucket      AlgorithmType = "leaky_bucket"
	AlgoConcurrency      AlgorithmType = "concurrency"
	AlgoQuota            AlgorithmType = "quota"
)

type AlgorithmConfig struct {
	Type           AlgorithmType `json:"type"`
	Limit          int64         `json:"limit"`
	Window         string        `json:"window"`
	Burst          int64         `json:"burst"`
	RefillRate     float64       `json:"refill_rate"`
	LeakRate       float64       `json:"leak_rate"`
	SubWindowCount int           `json:"sub_window_count"`
	MaxConcurrency int64         `json:"max_concurrency"`
	QuotaPeriod    string        `json:"quota_period"`
	CostPerRequest int64         `json:"cost_per_request"`
}

// FailureMode defines how to behave when the engine cannot evaluate normally.
type FailureMode string

// Supported failure handling modes.
const (
	FailOpen   FailureMode = "fail_open"
	FailClosed FailureMode = "fail_closed"
)

type EnforcementMode string

// Supported enforcement modes.
const (
	EnforceMode EnforcementMode = "enforce"
	ShadowMode  EnforcementMode = "shadow"
)

// Policy is the canonical configuration object evaluated by the engine.
type Policy struct {
	PolicyID        string            `json:"policy_id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Enabled         bool              `json:"enabled"`
	Priority        int               `json:"priority"`
	Scope           PolicyScope       `json:"scope"`
	Algorithm       AlgorithmConfig   `json:"algorithm"`
	Action          ActionType        `json:"action"`
	FailureMode     FailureMode       `json:"failure_mode"`
	EnforcementMode EnforcementMode   `json:"enforcement_mode"`
	RolloutPercent  int               `json:"rollout_percent"`
	ValidFromUnix   int64             `json:"valid_from_unix"`
	ValidToUnix     int64             `json:"valid_to_unix"`
	Metadata        map[string]string `json:"metadata"`
}

// PolicyAuditEntry records who changed a policy and when.
type PolicyAuditEntry struct {
	AuditID       string  `json:"audit_id"`
	PolicyID      string  `json:"policy_id"`
	ActionType    string  `json:"action_type"`
	ChangedBy     string  `json:"changed_by,omitempty"`
	ChangedAtUnix int64   `json:"changed_at_unix"`
	OldValue      *Policy `json:"old_value,omitempty"`
	NewValue      *Policy `json:"new_value,omitempty"`
}

// PolicyVersion stores immutable snapshots for rollback and history views.
type PolicyVersion struct {
	PolicyID      string `json:"policy_id"`
	Version       int64  `json:"version"`
	CreatedAtUnix int64  `json:"created_at_unix"`
	Snapshot      Policy `json:"snapshot"`
}
