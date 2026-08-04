import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { App } from "./App";
import { AuthProvider } from "./lib/auth";
import { AdminClientProvider } from "./lib/client";
import { OperatorProvider } from "./lib/operator";
import "./index.css";

const root = document.getElementById("root");
if (!root) throw new Error("#root missing");

createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <AdminClientProvider>
          <OperatorProvider>
            <App />
          </OperatorProvider>
        </AdminClientProvider>
      </AuthProvider>
    </BrowserRouter>
  </StrictMode>,
);
