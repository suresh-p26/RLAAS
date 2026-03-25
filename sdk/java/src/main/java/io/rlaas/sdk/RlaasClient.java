package io.rlaas.sdk;

import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.rlaas.sdk.model.CheckRequest;
import io.rlaas.sdk.model.Decision;
import io.rlaas.sdk.model.Policy;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.List;
import java.util.Map;

public class RlaasClient {
    private final String baseUrl;
    private final HttpClient httpClient;
    private final ObjectMapper objectMapper;

    public RlaasClient(String baseUrl) {
        this(baseUrl, Duration.ofSeconds(5));
    }

    public RlaasClient(String baseUrl, Duration timeout) {
        this.baseUrl = baseUrl.replaceAll("/+$", "");
        this.httpClient = HttpClient.newBuilder().connectTimeout(timeout).build();
        this.objectMapper = new ObjectMapper();
    }

    public Decision checkLimit(CheckRequest request) throws IOException, InterruptedException {
        String body = objectMapper.writeValueAsString(request);
        HttpResponse<String> response = post("/rlaas/v1/check", body);
        return readJson(response, Decision.class);
    }

    public Map<String, Object> acquire(Map<String, Object> request) throws IOException, InterruptedException {
        HttpResponse<String> response = post("/rlaas/v1/acquire", objectMapper.writeValueAsString(request));
        return readMap(response);
    }

    public Map<String, Object> release(String leaseId) throws IOException, InterruptedException {
        HttpResponse<String> response = post("/rlaas/v1/release", objectMapper.writeValueAsString(Map.of("lease_id", leaseId)));
        return readMap(response);
    }

    public List<Map<String, Object>> listPolicies() throws IOException, InterruptedException {
        HttpResponse<String> response = get("/rlaas/v1/policies");
        return readList(response);
    }

    public Map<String, Object> getPolicy(String policyId) throws IOException, InterruptedException {
        HttpResponse<String> response = get("/rlaas/v1/policies/" + policyId);
        return readMap(response);
    }

    public Map<String, Object> createPolicy(Policy policy) throws IOException, InterruptedException {
        HttpResponse<String> response = post("/rlaas/v1/policies", objectMapper.writeValueAsString(policy));
        return readMap(response);
    }

    public Map<String, Object> updatePolicy(String policyId, Policy policy) throws IOException, InterruptedException {
        HttpResponse<String> response = put("/rlaas/v1/policies/" + policyId, objectMapper.writeValueAsString(policy));
        return readMap(response);
    }

    public void deletePolicy(String policyId) throws IOException, InterruptedException {
        HttpResponse<String> response = delete("/rlaas/v1/policies/" + policyId);
        ensureSuccess(response);
    }

    public Map<String, Object> validatePolicy(Policy policy) throws IOException, InterruptedException {
        HttpResponse<String> response = post("/rlaas/v1/policies/validate", objectMapper.writeValueAsString(policy));
        return readMap(response);
    }

    public Map<String, Object> updateRollout(String policyId, int rolloutPercent) throws IOException, InterruptedException {
        HttpResponse<String> response = post("/rlaas/v1/policies/" + policyId + "/rollout", objectMapper.writeValueAsString(Map.of("rollout_percent", rolloutPercent)));
        return readMap(response);
    }

    public Map<String, Object> rollbackPolicy(String policyId, long version) throws IOException, InterruptedException {
        HttpResponse<String> response = post("/rlaas/v1/policies/" + policyId + "/rollback", objectMapper.writeValueAsString(Map.of("version", version)));
        return readMap(response);
    }

    public List<Map<String, Object>> listPolicyAudit(String policyId) throws IOException, InterruptedException {
        HttpResponse<String> response = get("/rlaas/v1/policies/" + policyId + "/audit");
        return readList(response);
    }

    public List<Map<String, Object>> listPolicyVersions(String policyId) throws IOException, InterruptedException {
        HttpResponse<String> response = get("/rlaas/v1/policies/" + policyId + "/versions");
        return readList(response);
    }

    public Map<String, Object> analyticsSummary(Integer top) throws IOException, InterruptedException {
        String path = "/rlaas/v1/analytics/summary" + (top == null ? "" : "?top=" + top);
        HttpResponse<String> response = get(path);
        return readMap(response);
    }

    private HttpResponse<String> get(String path) throws IOException, InterruptedException {
        HttpRequest request = HttpRequest.newBuilder(URI.create(baseUrl + path)).GET().build();
        return httpClient.send(request, HttpResponse.BodyHandlers.ofString());
    }

    private HttpResponse<String> post(String path, String jsonBody) throws IOException, InterruptedException {
        HttpRequest request = HttpRequest.newBuilder(URI.create(baseUrl + path))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(jsonBody))
                .build();
        return httpClient.send(request, HttpResponse.BodyHandlers.ofString());
    }

    private HttpResponse<String> put(String path, String jsonBody) throws IOException, InterruptedException {
        HttpRequest request = HttpRequest.newBuilder(URI.create(baseUrl + path))
                .header("Content-Type", "application/json")
                .PUT(HttpRequest.BodyPublishers.ofString(jsonBody))
                .build();
        return httpClient.send(request, HttpResponse.BodyHandlers.ofString());
    }

    private HttpResponse<String> delete(String path) throws IOException, InterruptedException {
        HttpRequest request = HttpRequest.newBuilder(URI.create(baseUrl + path)).DELETE().build();
        return httpClient.send(request, HttpResponse.BodyHandlers.ofString());
    }

    private <T> T readJson(HttpResponse<String> response, Class<T> type) throws IOException {
        ensureSuccess(response);
        return objectMapper.readValue(response.body(), type);
    }

    private Map<String, Object> readMap(HttpResponse<String> response) throws IOException {
        ensureSuccess(response);
        if (response.body() == null || response.body().isBlank()) {
            return Map.of();
        }
        return objectMapper.readValue(response.body(), new TypeReference<Map<String, Object>>() {});
    }

    private List<Map<String, Object>> readList(HttpResponse<String> response) throws IOException {
        ensureSuccess(response);
        if (response.body() == null || response.body().isBlank()) {
            return List.of();
        }
        return objectMapper.readValue(response.body(), new TypeReference<List<Map<String, Object>>>() {});
    }

    private void ensureSuccess(HttpResponse<String> response) {
        int code = response.statusCode();
        if (code >= 400) {
            throw new RuntimeException("RLAAS API error (" + code + "): " + response.body());
        }
    }
}
