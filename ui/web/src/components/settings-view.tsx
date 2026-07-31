import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input, Textarea } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select } from "@/components/ui/select";

export type CredentialForm = {
  apiKey: string;
  baseUrl: string;
  extraHeaders: string;
};

export type ProviderForm = {
  kind: string;
  name: string;
  config: string;
};

export function SettingsView({
  credential,
  onCredentialChange,
  onSaveCredential,
  provider,
  onProviderChange,
  onRegisterProvider,
}: {
  credential: CredentialForm;
  onCredentialChange: (next: CredentialForm) => void;
  onSaveCredential: () => Promise<void>;
  provider: ProviderForm;
  onProviderChange: (next: ProviderForm) => void;
  onRegisterProvider: () => Promise<void>;
}) {
  return (
    <section className="space-y-4">
      <h2 className="text-2xl font-semibold">Org settings</h2>
      <Card>
        <CardHeader>
          <CardTitle>Inference credential</CardTitle>
          <CardDescription>Values are write-only and encrypted at rest; they are never returned.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <Label>
            <span>OpenAI API key</span>
            <Input
              aria-label="OpenAI API key"
              type="password"
              value={credential.apiKey}
              onChange={(e) => onCredentialChange({ ...credential, apiKey: e.target.value })}
            />
          </Label>
          <Label>
            <span>Base URL</span>
            <Input
              aria-label="Base URL"
              value={credential.baseUrl}
              placeholder="https://gateway.example.com/v1"
              onChange={(e) => onCredentialChange({ ...credential, baseUrl: e.target.value })}
            />
          </Label>
          <Label>
            <span>Extra headers (JSON)</span>
            <Textarea
              aria-label="Extra headers JSON"
              rows={6}
              value={credential.extraHeaders}
              onChange={(e) => onCredentialChange({ ...credential, extraHeaders: e.target.value })}
            />
            <span className="block text-xs text-zinc-500">
              Example: {`{"cf-aig-collect-log-payload":"false"}`}
            </span>
          </Label>
          <Button onClick={onSaveCredential}>Save credential</Button>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Provider instances</CardTitle>
          <CardDescription>Where this org's environments run.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <Label>
            <span>Provider kind</span>
            <Select
              aria-label="Provider kind"
              value={provider.kind}
              onChange={(e) => onProviderChange({ ...provider, kind: e.target.value })}
            >
              <option value="byo_k8s">Kubernetes</option>
              <option value="hosted_eks">Hosted EKS</option>
              <option value="byo_nomad">Nomad</option>
              <option value="tunnel_local">Local tunnel</option>
            </Select>
          </Label>
          <Label>
            <span>Provider name</span>
            <Input
              aria-label="Provider name"
              value={provider.name}
              onChange={(e) => onProviderChange({ ...provider, name: e.target.value })}
            />
          </Label>
          <Label>
            <span>Provider config JSON</span>
            <Textarea
              aria-label="Provider config JSON"
              rows={4}
              value={provider.config}
              onChange={(e) => onProviderChange({ ...provider, config: e.target.value })}
            />
          </Label>
          <Button onClick={onRegisterProvider}>Register provider</Button>
        </CardContent>
      </Card>
    </section>
  );
}
