import type { AuthProvider } from "@refinedev/core";
import { httpClient, API_URL, setCSRFToken } from "./httpClient";

// authProvider bridges Refine's auth flow to the BFF session endpoints. The
// session is a cookie set by /api/login; check()/getIdentity read /api/session,
// which also returns the CSRF token the httpClient attaches to mutations.
export const authProvider: AuthProvider = {
  login: async ({ username, password }) => {
    try {
      const res = await httpClient.post(`${API_URL}/login`, { username, password });
      setCSRFToken(res.data?.csrf_token);
      return { success: true, redirectTo: "/" };
    } catch {
      return {
        success: false,
        error: { name: "Login failed", message: "Invalid username or password." },
      };
    }
  },
  logout: async () => {
    try {
      await httpClient.post(`${API_URL}/logout`);
    } catch {
      // ignore — clearing the cookie is best-effort
    }
    setCSRFToken("");
    return { success: true, redirectTo: "/login" };
  },
  check: async () => {
    try {
      const res = await httpClient.get(`${API_URL}/session`);
      setCSRFToken(res.data?.csrf_token);
      return { authenticated: true };
    } catch {
      return { authenticated: false, redirectTo: "/login" };
    }
  },
  getIdentity: async () => {
    try {
      const res = await httpClient.get(`${API_URL}/session`);
      return { name: res.data?.username as string };
    } catch {
      return null;
    }
  },
  onError: async (error) => {
    if (error?.response?.status === 401) {
      return { logout: true, redirectTo: "/login" };
    }
    return {};
  },
};
