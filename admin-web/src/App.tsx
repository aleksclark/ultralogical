import type { ReactNode } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { Shell } from "@/components/Shell";
import { useAuth } from "@/lib/auth";
import { APIKeysPage } from "@/pages/APIKeysPage";
import { AutomationPage } from "@/pages/AutomationPage";
import { CredentialsPage } from "@/pages/CredentialsPage";
import { EventsPage } from "@/pages/EventsPage";
import { InternalsPage } from "@/pages/InternalsPage";
import { JobDetailPage } from "@/pages/JobDetailPage";
import { JobsPage } from "@/pages/JobsPage";
import { LoginPage } from "@/pages/LoginPage";
import { MemoryPage } from "@/pages/MemoryPage";
import { OverviewPage } from "@/pages/OverviewPage";
import { ProviderDetailPage } from "@/pages/ProviderDetailPage";
import { ProvidersPage } from "@/pages/ProvidersPage";
import { ResourceDetailPage } from "@/pages/ResourceDetailPage";
import { ResourcesPage } from "@/pages/ResourcesPage";
import { RunDetailPage } from "@/pages/RunDetailPage";
import { RunsPage } from "@/pages/RunsPage";
import { SecurityPage } from "@/pages/SecurityPage";
import { SessionDetailPage } from "@/pages/SessionDetailPage";
import { SessionsPage } from "@/pages/SessionsPage";
import { TenantDetailPage } from "@/pages/TenantDetailPage";
import { TenantsPage } from "@/pages/TenantsPage";
import { WaitsPage } from "@/pages/WaitsPage";

function RequireAuth({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth();
  if (!isAuthenticated) return <Navigate to="/login" replace />;
  return children;
}

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        element={
          <RequireAuth>
            <Shell />
          </RequireAuth>
        }
      >
        <Route index element={<OverviewPage />} />
        <Route path="tenants" element={<TenantsPage />} />
        <Route path="tenants/:id" element={<TenantDetailPage />} />
        <Route path="sessions" element={<SessionsPage />} />
        <Route path="sessions/:id" element={<SessionDetailPage />} />
        <Route path="events" element={<EventsPage />} />
        <Route path="runs" element={<RunsPage />} />
        <Route path="runs/:id" element={<RunDetailPage />} />
        <Route path="resources" element={<ResourcesPage />} />
        <Route path="resources/:id" element={<ResourceDetailPage />} />
        <Route path="providers" element={<ProvidersPage />} />
        <Route path="providers/:id" element={<ProviderDetailPage />} />
        <Route path="jobs" element={<JobsPage />} />
        <Route path="jobs/:id" element={<JobDetailPage />} />
        <Route path="automation" element={<AutomationPage />} />
        <Route path="memory" element={<MemoryPage />} />
        <Route path="waits" element={<WaitsPage />} />
        <Route path="credentials" element={<CredentialsPage />} />
        <Route path="api-keys" element={<APIKeysPage />} />
        <Route path="security" element={<SecurityPage />} />
        <Route path="internals" element={<InternalsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
