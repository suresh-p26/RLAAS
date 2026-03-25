import { CheckRequest, Decision, Policy } from "./types";

export class RlaasClient {
  private readonly baseUrl: string;

  constructor(baseUrl: string) {
    this.baseUrl = baseUrl.replace(/\/+$/, "");
  }

  async checkLimit(req: CheckRequest): Promise<Decision> {
    return this.post<Decision>("/rlaas/v1/check", req);
  }

  async acquire(request: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post("/rlaas/v1/acquire", request);
  }

  async release(leaseId: string): Promise<Record<string, unknown>> {
    return this.post("/rlaas/v1/release", { lease_id: leaseId });
  }

  async listPolicies(): Promise<Record<string, unknown>[]> {
    return this.get("/rlaas/v1/policies");
  }

  async getPolicy(policyId: string): Promise<Record<string, unknown>> {
    return this.get(`/rlaas/v1/policies/${policyId}`);
  }

  async createPolicy(policy: Policy): Promise<Record<string, unknown>> {
    return this.post("/rlaas/v1/policies", policy);
  }

  async updatePolicy(policyId: string, policy: Policy): Promise<Record<string, unknown>> {
    return this.put(`/rlaas/v1/policies/${policyId}`, policy);
  }

  async deletePolicy(policyId: string): Promise<void> {
    await this.del(`/rlaas/v1/policies/${policyId}`);
  }

  async validatePolicy(policy: Policy): Promise<Record<string, unknown>> {
    return this.post("/rlaas/v1/policies/validate", policy);
  }

  async updateRollout(policyId: string, rolloutPercent: number): Promise<Record<string, unknown>> {
    return this.post(`/rlaas/v1/policies/${policyId}/rollout`, { rollout_percent: rolloutPercent });
  }

  async rollbackPolicy(policyId: string, version: number): Promise<Record<string, unknown>> {
    return this.post(`/rlaas/v1/policies/${policyId}/rollback`, { version });
  }

  async listPolicyAudit(policyId: string): Promise<Record<string, unknown>[]> {
    return this.get(`/rlaas/v1/policies/${policyId}/audit`);
  }

  async listPolicyVersions(policyId: string): Promise<Record<string, unknown>[]> {
    return this.get(`/rlaas/v1/policies/${policyId}/versions`);
  }

  async analyticsSummary(top?: number): Promise<Record<string, unknown>> {
    const suffix = typeof top === "number" ? `?top=${top}` : "";
    return this.get(`/rlaas/v1/analytics/summary${suffix}`);
  }

  private async get<T>(path: string): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`);
    return this.decode<T>(res);
  }

  private async post<T>(path: string, body: unknown): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return this.decode<T>(res);
  }

  private async put<T>(path: string, body: unknown): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return this.decode<T>(res);
  }

  private async del(path: string): Promise<void> {
    const res = await fetch(`${this.baseUrl}${path}`, { method: "DELETE" });
    if (!res.ok) {
      throw new Error(`RLAAS API error (${res.status})`);
    }
  }

  private async decode<T>(res: Response): Promise<T> {
    if (!res.ok) {
      throw new Error(`RLAAS API error (${res.status})`);
    }
    if (res.status === 204) {
      return undefined as T;
    }
    return (await res.json()) as T;
  }
}
