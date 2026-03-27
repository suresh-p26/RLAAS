use serde::{Deserialize, Serialize};
use std::collections::HashMap;

// ── Request ───────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Default, Serialize)]
pub struct CheckRequest {
    pub request_id:  String,
    pub org_id:      String,
    pub tenant_id:   String,
    pub signal_type: String,
    pub operation:   String,
    pub endpoint:    String,
    pub method:      String,

    #[serde(skip_serializing_if = "String::is_empty")]
    pub user_id:     String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub api_key:     String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub client_id:   String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub source_ip:   String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub region:      String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub environment: String,

    #[serde(skip_serializing_if = "HashMap::is_empty")]
    pub tags: HashMap<String, String>,
}

// ── Decision ──────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Deserialize)]
pub struct Decision {
    pub allowed:        bool,
    pub action:         String,
    pub reason:         String,
    #[serde(default)]
    pub remaining:      i64,
    #[serde(default)]
    pub retry_after_ms: i64,
    #[serde(default)]
    pub policy_id:      String,
}

// ── Policy ────────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Policy {
    pub policy_id:        String,
    pub name:             String,
    pub enabled:          bool,
    #[serde(default)]
    pub priority:         i32,
    pub scope:            serde_json::Value,
    pub algorithm:        serde_json::Value,
    pub action:           String,
    #[serde(default = "default_failure_mode")]
    pub failure_mode:     String,
    #[serde(default = "default_enforcement_mode")]
    pub enforcement_mode: String,
    #[serde(default = "default_rollout")]
    pub rollout_percent:  i32,
}

fn default_failure_mode()     -> String { "fail_open".into() }
fn default_enforcement_mode() -> String { "enforce".into() }
fn default_rollout()          -> i32    { 100 }

// ── Analytics ─────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Deserialize)]
pub struct AnalyticsSummary {
    pub top: Vec<PolicyStat>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct PolicyStat {
    pub policy_id: String,
    #[serde(default)]
    pub allowed:   i64,
    #[serde(default)]
    pub denied:    i64,
}
