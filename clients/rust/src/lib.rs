pub mod ultra {
    pub mod v1 {
        tonic::include_proto!("ultra.v1");
    }
}

use tonic::{Request, Status, metadata::MetadataValue};

#[derive(Clone)]
pub struct Auth {
    token: MetadataValue<tonic::metadata::Ascii>,
}

impl Auth {
    pub fn new(token: &str) -> Result<Self, Status> {
        let value = format!("Bearer {token}").parse().map_err(|_| Status::invalid_argument("invalid token"))?;
        Ok(Self { token: value })
    }

    pub fn request<T>(&self, message: T) -> Request<T> {
        let mut request = Request::new(message);
        request.metadata_mut().insert("authorization", self.token.clone());
        request
    }
}
