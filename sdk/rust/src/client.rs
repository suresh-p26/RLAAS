use crate::error::RlaasError;
use crate::types::{AnalyticsSummary, CheckRequest, Decision, Policy};
use reqwest::{Client as HttpClient, StatusCode};
use serde_json::Value;
use std::time::Duration;

/// Async HTTP client for the RLAAS API.
///
/// # Example
/// ```no_run
/// # tokio_test::block_on(async {
/// use rlaas_sdk::{Client, CheckRequest};
///
/// let client = Client::new("http://localhost:8080");
/// let decision = client.check(&CheckRequest {
///     request_id:  "r1".into(),
///     org_id:      "acme".into(),
///     signal_type: "http".into(),
///     ..Default::default()
/// }).await.unwrap();
/// assert!(decision.allowed);
/// # })
/// ```
#[derive(Clone)]
pub struct Client {
    base_url: String,
    http:     HttpClient,
}

impl Client {
    /// Create a client with a 5-second default timeout.
    pub fn new(base_url: impl Into<String>) -> Self {
        Self::with_timeout(base_url, Duration::from_secs(5))
    }

    pub fn with_timeout(base_url: impl Into<String>, timeout: Duration) -> Self {
        let http = HttpClient::builder()
            .timeout(timeout)
            .build()
            .expect("failed to build reqwest client");
        Self {
            base_url: base_url.into().trim_end_matches('/').to_owned(),
            http,
        }
    }

    // ── Decision API ─────────────────────────────────────────────────────────

    pub async fn check(&self, req: &CheckRequest) -> Result<Decision, RlaasError> {
        let resp = self.post("/rlaas/v1/check", req).await?;
        Ok(resp)
    }

    pub async fn acquire(&self, body: &Value) -> Result<Value, RlaasError> {
        self.post("/rlaas/v1/acquire", body).await
    }

    pub async fn release(&self, lease_id: &str) -> Result<Value, RlaasError> {
        self.post("/rlaas/v1/release", &serde_json::json!({ "lease_id": lease_id })).await
    }

    // ── Policy management ─────────────────────────────────────────────────────

    pub async fn list_policies(&self) -> Result<Vec<Policy>, RlaasError> {
        self.get("/rlaas/v1/policies").await
    }

    pub async fn get_policy(&self, policy_id: &str) -> Result<Policy, RlaasError> {
        self.get(&format!("/rlaas/v1/policies/{policy_id}")).await
    }

    pub async fn create_policy(&self, policy: &Policy) -> Result<Value, RlaasError> {
        self.post(&format!("/rlaas/v1/policies"), policy).await
    }

    pub async fn update_policy(&self, policy_id: &str, policy: &Policy) -> Result<Value, RlaasError> {
        self.put(&format!("/rlaas/v1/policies/{policy_id}"), policy).await
    }

    pub async fn delete_policy(&self, policy_id: &str) -> Result<(), RlaasError> {
        let url = format!("{}/rlaas/v1/policies/{policy_id}", self.base_url);
        let resp = self.http.delete(&url).send().await?;
        Self::check_status(resp.status(), || async { Ok(String::new()) }).await?;
        Ok(())
    }

    pub async fn validate_policy(&self, policy: &Policy) -> Result<Value, RlaasError> {
        self.post("/rlaas/v1/policies/validate", policy).await
    }

    // ── Lifecycle ─────────────────────────────────────────────────────────────

    pub async fn update_rollout(&self, policy_id: &str, rollout_percent: u8) -> Result<Value, RlaasError> {
        self.post(
            &format!("/rlaas/v1/policies/{policy_id}/rollout"),
            &serde_json::json!({ "rollout_percent": rollout_percent }),
        ).await
    }

    pub async fn rollback_policy(&self, policy_id: &str, version: i64) -> Result<Value, RlaasError> {
        self.post(
            &format!("/rlaas/v1/policies/{policy_id}/rollback"),
            &serde_json::json!({ "version": version }),
        ).await
    }

    // ── History ───────────────────────────────────────────────────────────────

    pub async fn list_policy_audit(&self, policy_id: &str) -> Result<Vec<Value>, RlaasError> {
        self.get(&format!("/rlaas/v1/policies/{policy_id}/audit")).await
    }

    pub async fn list_policy_versions(&self, policy_id: &str) -> Result<Vec<Value>, RlaasError> {
        self.get(&format!("/rlaas/v1/policies/{policy_id}/versions")).await
    }

    // ── Analytics ─────────────────────────────────────────────────────────────

    pub async fn analytics_summary(&self, top: Option<u32>) -> Result<AnalyticsSummary, RlaasError> {
        let path = match top {
            Some(n) => format!("/rlaas/v1/analytics/summary?top={n}"),
            None    => "/rlaas/v1/analytics/summary".to_owned(),
        };
        self.get(&path).await
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    async fn get<T: serde::de::DeserializeOwned>(&self, path: &str) -> Result<T, RlaasError> {
        let url = format!("{}{}", self.base_url, path);
        let resp = self.http.get(&url).send().await?;
        let status = resp.status();
        let body = resp.text().await?;
        Self::check_status(status, || async { Ok(body.clone()) }).await?;
        Ok(serde_json::from_str(&body)?)
    }

    async fn post<B, T>(&self, path: &str, body: &B) -> Result<T, RlaasError>
    where
        B: serde::Serialize,
        T: serde::de::DeserializeOwned,
    {
        let url = format!("{}{}", self.base_url, path);
        let resp = self.http.post(&url).json(body).send().await?;
        let status = resp.status();
        let text = resp.text().await?;
        Self::check_status(status, || async { Ok(text.clone()) }).await?;
        Ok(serde_json::from_str(&text)?)
    }

    async fn put<B, T>(&self, path: &str, body: &B) -> Result<T, RlaasError>
    where
        B: serde::Serialize,
        T: serde::de::DeserializeOwned,
    {
        let url = format!("{}{}", self.base_url, path);
        let resp = self.http.put(&url).json(body).send().await?;
        let status = resp.status();
        let text = resp.text().await?;
        Self::check_status(status, || async { Ok(text.clone()) }).await?;
        Ok(serde_json::from_str(&text)?)
    }

    async fn check_status<F, Fut>(status: StatusCode, body: F) -> Result<(), RlaasError>
    where
        F: Fn() -> Fut,
        Fut: std::future::Future<Output = Result<String, RlaasError>>,
    {
        if status.is_client_error() || status.is_server_error() {
            let text = body().await.unwrap_or_default();
            let message = serde_json::from_str::<Value>(&text)
                .ok()
                .and_then(|v| v["error"].as_str().map(|s| s.to_owned()))
                .unwrap_or(text);
            return Err(RlaasError::Api { status: status.as_u16(), message });
        }
        Ok(())
    }
}
