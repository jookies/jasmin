import { useEffect, useState } from "react";

import { httpClient, API_URL } from "./httpClient";

// Features mirrors the server's optional capabilities, reported by /api/session.
// The UI hides what this deployment did not enable, so it never offers an action
// the server would refuse.
export type Features = {
  interceptor_editing: boolean;
};

const noFeatures: Features = { interceptor_editing: false };

// useFeatures reads the capability set once per mount. A 401 (not logged in
// yet) simply yields no features; the session endpoint is re-read after login
// because the component remounts under the authenticated layout.
export const useFeatures = (): Features => {
  const [features, setFeatures] = useState<Features>(noFeatures);

  useEffect(() => {
    let cancelled = false;
    httpClient
      .get(`${API_URL}/session`)
      .then((res) => {
        if (!cancelled) setFeatures(res.data?.features ?? noFeatures);
      })
      .catch(() => {
        if (!cancelled) setFeatures(noFeatures);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return features;
};
