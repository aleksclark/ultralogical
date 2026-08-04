import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { App } from "./App";
import { AuthProvider } from "./lib/auth";
import { AdminClientProvider } from "./lib/client";
import "./index.css";

const root = document.getElementById("root");
if (!root) throw new Error("#root missing");

createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <AdminClientProvider>
          <App />
        </AdminClientProvider>
      </AuthProvider>
    </BrowserRouter>
  </StrictMode>,
);
