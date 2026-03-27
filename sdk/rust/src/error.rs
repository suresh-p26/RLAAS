use thiserror::Error;

#[derive(Debug, Error)]
pub enum RlaasError {
    #[error("RLAAS API error ({status}): {message}")]
    Api { status: u16, message: String },

    #[error("HTTP transport error: {0}")]
    Http(#[from] reqwest::Error),

    #[error("JSON error: {0}")]
    Json(#[from] serde_json::Error),
}
