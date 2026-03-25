using System.Net.Http.Json;
using System.Text;
using System.Text.Json;
using Rlaas.Sdk.Models;

namespace Rlaas.Sdk;

public sealed class RlaasClient
{
    private readonly HttpClient _httpClient;
    private readonly string _baseUrl;
    private readonly JsonSerializerOptions _jsonOptions = new(JsonSerializerDefaults.Web)
    {
        PropertyNameCaseInsensitive = true
    };

    public RlaasClient(string baseUrl, HttpClient? httpClient = null)
    {
        _baseUrl = baseUrl.TrimEnd('/');
        _httpClient = httpClient ?? new HttpClient { Timeout = TimeSpan.FromSeconds(5) };
    }

    public Task<Decision> CheckLimitAsync(CheckRequest request, CancellationToken cancellationToken = default) =>
        SendAsync<Decision>(HttpMethod.Post, "/rlaas/v1/check", request, cancellationToken);

    public Task<Dictionary<string, object>> AcquireAsync(Dictionary<string, object> request, CancellationToken cancellationToken = default) =>
        SendAsync<Dictionary<string, object>>(HttpMethod.Post, "/rlaas/v1/acquire", request, cancellationToken);

    public Task<Dictionary<string, object>> ReleaseAsync(string leaseId, CancellationToken cancellationToken = default) =>
        SendAsync<Dictionary<string, object>>(HttpMethod.Post, "/rlaas/v1/release", new Dictionary<string, string> { ["lease_id"] = leaseId }, cancellationToken);

    public Task<List<Dictionary<string, object>>> ListPoliciesAsync(CancellationToken cancellationToken = default) =>
        SendAsync<List<Dictionary<string, object>>>(HttpMethod.Get, "/rlaas/v1/policies", null, cancellationToken);

    public Task<Dictionary<string, object>> GetPolicyAsync(string policyId, CancellationToken cancellationToken = default) =>
        SendAsync<Dictionary<string, object>>(HttpMethod.Get, $"/rlaas/v1/policies/{policyId}", null, cancellationToken);

    public Task<Dictionary<string, object>> CreatePolicyAsync(Policy policy, CancellationToken cancellationToken = default) =>
        SendAsync<Dictionary<string, object>>(HttpMethod.Post, "/rlaas/v1/policies", policy, cancellationToken);

    public Task<Dictionary<string, object>> UpdatePolicyAsync(string policyId, Policy policy, CancellationToken cancellationToken = default) =>
        SendAsync<Dictionary<string, object>>(HttpMethod.Put, $"/rlaas/v1/policies/{policyId}", policy, cancellationToken);

    public async Task DeletePolicyAsync(string policyId, CancellationToken cancellationToken = default)
    {
        using var request = new HttpRequestMessage(HttpMethod.Delete, _baseUrl + $"/rlaas/v1/policies/{policyId}");
        using var response = await _httpClient.SendAsync(request, cancellationToken);
        EnsureSuccess(response);
    }

    public Task<Dictionary<string, object>> ValidatePolicyAsync(Policy policy, CancellationToken cancellationToken = default) =>
        SendAsync<Dictionary<string, object>>(HttpMethod.Post, "/rlaas/v1/policies/validate", policy, cancellationToken);

    public Task<Dictionary<string, object>> UpdateRolloutAsync(string policyId, int rolloutPercent, CancellationToken cancellationToken = default) =>
        SendAsync<Dictionary<string, object>>(HttpMethod.Post, $"/rlaas/v1/policies/{policyId}/rollout", new Dictionary<string, int> { ["rollout_percent"] = rolloutPercent }, cancellationToken);

    public Task<Dictionary<string, object>> RollbackPolicyAsync(string policyId, long version, CancellationToken cancellationToken = default) =>
        SendAsync<Dictionary<string, object>>(HttpMethod.Post, $"/rlaas/v1/policies/{policyId}/rollback", new Dictionary<string, long> { ["version"] = version }, cancellationToken);

    public Task<List<Dictionary<string, object>>> ListPolicyAuditAsync(string policyId, CancellationToken cancellationToken = default) =>
        SendAsync<List<Dictionary<string, object>>>(HttpMethod.Get, $"/rlaas/v1/policies/{policyId}/audit", null, cancellationToken);

    public Task<List<Dictionary<string, object>>> ListPolicyVersionsAsync(string policyId, CancellationToken cancellationToken = default) =>
        SendAsync<List<Dictionary<string, object>>>(HttpMethod.Get, $"/rlaas/v1/policies/{policyId}/versions", null, cancellationToken);

    public Task<Dictionary<string, object>> AnalyticsSummaryAsync(int? top = null, CancellationToken cancellationToken = default)
    {
        var suffix = top.HasValue ? $"?top={top.Value}" : string.Empty;
        return SendAsync<Dictionary<string, object>>(HttpMethod.Get, "/rlaas/v1/analytics/summary" + suffix, null, cancellationToken);
    }

    private async Task<T> SendAsync<T>(HttpMethod method, string path, object? body, CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(method, _baseUrl + path);
        if (body is not null)
        {
            var json = JsonSerializer.Serialize(body, _jsonOptions);
            request.Content = new StringContent(json, Encoding.UTF8, "application/json");
        }

        using var response = await _httpClient.SendAsync(request, cancellationToken);
        EnsureSuccess(response);

        if (response.Content is null)
        {
            return default!;
        }

        var payload = await response.Content.ReadFromJsonAsync<T>(_jsonOptions, cancellationToken);
        return payload!;
    }

    private static void EnsureSuccess(HttpResponseMessage response)
    {
        if ((int)response.StatusCode >= 400)
        {
            throw new InvalidOperationException($"RLAAS API error ({(int)response.StatusCode})");
        }
    }
}
