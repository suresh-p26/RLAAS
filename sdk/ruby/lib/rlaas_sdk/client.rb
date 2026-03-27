require "net/http"
require "json"
require "uri"

module Rlaas
  class Client
    # @param base_url [String]  e.g. "http://localhost:8080"
    # @param timeout  [Integer] open/read timeout in seconds
    def initialize(base_url, timeout: 5)
      uri = URI.parse(base_url.chomp("/"))
      @http = Net::HTTP.new(uri.host, uri.port)
      @http.use_ssl       = uri.scheme == "https"
      @http.open_timeout  = timeout
      @http.read_timeout  = timeout
      @http.start
    end

    # ── Decision API ────────────────────────────────────────────────────────

    # @param req [Hash]  :request_id, :org_id, :tenant_id, :signal_type,
    #                    :operation, :endpoint, :method, :user_id, :tags, …
    # @return [Decision]
    def check(req)
      data = post("/rlaas/v1/check", compact(req))
      Decision.new(
        allowed:        data["allowed"],
        action:         data["action"],
        reason:         data["reason"],
        remaining:      data["remaining"].to_i,
        retry_after:    data["retry_after"].to_s,
        policy_id:      data["policy_id"].to_s,
      )
    end

    def acquire(body)
      post("/rlaas/v1/acquire", body)
    end

    def release(lease_id)
      post("/rlaas/v1/release", { lease_id: lease_id })
    end

    # ── Policy management ────────────────────────────────────────────────────

    def list_policies
      get("/rlaas/v1/policies")
    end

    def get_policy(policy_id)
      get("/rlaas/v1/policies/#{policy_id}")
    end

    def create_policy(policy)
      post("/rlaas/v1/policies", policy)
    end

    def update_policy(policy_id, policy)
      put("/rlaas/v1/policies/#{policy_id}", policy)
    end

    def delete_policy(policy_id)
      delete_req("/rlaas/v1/policies/#{policy_id}")
    end

    def validate_policy(policy)
      post("/rlaas/v1/policies/validate", policy)
    end

    # ── Lifecycle ────────────────────────────────────────────────────────────

    def update_rollout(policy_id, rollout_percent)
      post("/rlaas/v1/policies/#{policy_id}/rollout", { rollout_percent: rollout_percent })
    end

    def rollback_policy(policy_id, version)
      post("/rlaas/v1/policies/#{policy_id}/rollback", { version: version })
    end

    # ── History ──────────────────────────────────────────────────────────────

    def list_policy_audit(policy_id)
      get("/rlaas/v1/policies/#{policy_id}/audit")
    end

    def list_policy_versions(policy_id)
      get("/rlaas/v1/policies/#{policy_id}/versions")
    end

    # ── Analytics ────────────────────────────────────────────────────────────

    def analytics_summary(top: nil)
      path = "/rlaas/v1/analytics/summary"
      path += "?top=#{top}" if top
      get(path)
    end

    def close
      @http.finish if @http.started?
    end

    private

    def get(path)
      req = Net::HTTP::Get.new(path, json_headers)
      decode(@http.request(req))
    end

    def post(path, body)
      req = Net::HTTP::Post.new(path, json_headers)
      req.body = JSON.generate(body)
      decode(@http.request(req))
    end

    def put(path, body)
      req = Net::HTTP::Put.new(path, json_headers)
      req.body = JSON.generate(body)
      decode(@http.request(req))
    end

    def delete_req(path)
      req = Net::HTTP::Delete.new(path, json_headers)
      resp = @http.request(req)
      raise_for_status(resp)
      nil
    end

    def json_headers
      { "Content-Type" => "application/json", "Accept" => "application/json" }
    end

    def decode(resp)
      raise_for_status(resp)
      return nil if resp.body.nil? || resp.body.strip.empty?
      JSON.parse(resp.body)
    end

    def raise_for_status(resp)
      return if resp.code.to_i < 400
      msg = resp.body.then { |b|
        parsed = JSON.parse(b) rescue nil
        parsed.is_a?(Hash) && parsed["error"] ? parsed["error"] : b.to_s
      }
      raise Rlaas::Error.new(resp.code.to_i, msg)
    end

    def compact(hash)
      hash.reject { |_, v| v.nil? || v == "" }
    end
  end
end
