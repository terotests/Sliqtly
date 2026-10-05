use thiserror::Error;

pub type Result<T> = std::result::Result<T, Error>;

#[derive(Debug, Error)]
pub enum Error {
    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),

    #[error("serialization error: {0}")]
    Serialization(#[from] serde_json::error::Error),

    #[error("not found")]
    NotFound,

    #[error("conflict: CAS mismatch")]
    Conflict,

    #[error("transaction error: {0}")]
    Transaction(String),

    #[error("codec error: {0}")]
    Codec(String),

    #[error("invalid argument: {0}")]
    InvalidArgument(String),

    #[error("internal error: {0}")]
    Internal(String),
}
