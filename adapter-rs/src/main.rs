mod models;
mod routes;
mod state;

use std::net::SocketAddr;

use axum::Router;
use state::AppState;

#[tokio::main]
async fn main() {
    let state = AppState::new();
    let app: Router = routes::router().with_state(state);

    let addr: SocketAddr = "127.0.0.1:8091".parse().expect("invalid addr");
    let listener = tokio::net::TcpListener::bind(addr)
        .await
        .expect("bind failed");

    println!("adapter-rs listening on http://{}", addr);
    axum::serve(listener, app).await.expect("server failed");
}
