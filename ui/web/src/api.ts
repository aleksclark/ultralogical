import { createClient, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AgentService } from "@client/gen/ultra/v1/agent_pb";
import { EnvService } from "@client/gen/ultra/v1/env_pb";
import { EventService } from "@client/gen/ultra/v1/event_pb";
import { OrgService } from "@client/gen/ultra/v1/org_pb";
import { SessionService } from "@client/gen/ultra/v1/session_pb";

export function clients(baseUrl: string, token: string) {
  const auth: Interceptor = (next) => async (req) => {
    req.header.set("Authorization", `Bearer ${token}`);
    return next(req);
  };
  const transport = createConnectTransport({ baseUrl, interceptors: [auth] });
  return {
    orgs: createClient(OrgService, transport),
    sessions: createClient(SessionService, transport),
    events: createClient(EventService, transport),
    agents: createClient(AgentService, transport),
    envs: createClient(EnvService, transport),
  };
}
