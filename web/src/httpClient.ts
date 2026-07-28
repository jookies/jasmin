import axios from "axios";

// All BFF calls are same-origin under /api and rely on the session cookie
// (withCredentials). Mutations carry the per-session CSRF token in a header; the
// token is handed out by /api/login and /api/session and held in memory only.
export const API_URL = "/api";

let csrfToken = "";
export const setCSRFToken = (token: string) => {
  csrfToken = token || "";
};

export const httpClient = axios.create({
  withCredentials: true,
});

httpClient.interceptors.request.use((config) => {
  const method = (config.method ?? "get").toLowerCase();
  if (csrfToken && ["post", "put", "patch", "delete"].includes(method)) {
    config.headers.set("X-CSRF-Token", csrfToken);
  }
  return config;
});
