import { lazy, Suspense } from "react";
import { Refine, Authenticated } from "@refinedev/core";
import {
  ErrorComponent,
  useNotificationProvider,
} from "@refinedev/antd";
import "@refinedev/antd/dist/reset.css";
import routerProvider, {
  NavigateToResource,
  CatchAllNavigate,
  UnsavedChangesNotifier,
  DocumentTitleHandler,
} from "@refinedev/react-router-v6";
import { BrowserRouter, Routes, Route, Outlet } from "react-router-dom";
import { ConfigProvider, App as AntdApp } from "antd";
import dataProvider from "@refinedev/simple-rest";
import {
  ApiOutlined,
  BookOutlined,
  CodeOutlined,
  DashboardOutlined,
  FilterOutlined,
  FundProjectionScreenOutlined,
  GlobalOutlined,
  InboxOutlined,
  LinkOutlined,
  SafetyCertificateOutlined,
  ShareAltOutlined,
  TeamOutlined,
  UsergroupAddOutlined,
  UserOutlined,
} from "@ant-design/icons";

import { httpClient, API_URL } from "./httpClient";
import { authProvider } from "./authProvider";
import { useFeatures } from "./useFeatures";
import { AppShell } from "./components/AppShell";
import { PageLoading } from "./components/OperatorUI";

const DashboardPage = lazy(() =>
  import("./pages/dashboard").then((module) => ({ default: module.DashboardPage })),
);
const ConnectorList = lazy(() =>
  import("./pages/connectors").then((module) => ({ default: module.ConnectorList })),
);
const RouteList = lazy(() =>
  import("./pages/routes").then((module) => ({ default: module.RouteList })),
);
const MORouteList = lazy(() =>
  import("./pages/mo-routes").then((module) => ({ default: module.MORouteList })),
);
const FilterListPage = lazy(() =>
  import("./pages/filters").then((module) => ({ default: module.FilterListPage })),
);
const HTTPConnectorList = lazy(() =>
  import("./pages/http-connectors").then((module) => ({ default: module.HTTPConnectorList })),
);
const InterceptorList = lazy(() =>
  import("./pages/interceptors").then((module) => ({ default: module.InterceptorList })),
);
const GroupList = lazy(() =>
  import("./pages/groups").then((module) => ({ default: module.GroupList })),
);
const UserList = lazy(() =>
  import("./pages/users").then((module) => ({ default: module.UserList })),
);
const SMPPsUserList = lazy(() =>
  import("./pages/smpps-users").then((module) => ({ default: module.SMPPsUserList })),
);
const OperationsPage = lazy(() =>
  import("./pages/operations").then((module) => ({ default: module.OperationsPage })),
);
const ProfilesPage = lazy(() =>
  import("./pages/profiles").then((module) => ({ default: module.ProfilesPage })),
);
const EducationPage = lazy(() =>
  import("./pages/education").then((module) => ({ default: module.EducationPage })),
);
const PartnerOnboardingPage = lazy(() =>
  import("./pages/partner-onboarding").then((module) => ({
    default: module.PartnerOnboardingPage,
  })),
);
const LoginPage = lazy(() =>
  import("./pages/login").then((module) => ({ default: module.LoginPage })),
);

export default function App() {
  const features = useFeatures();

  // Interceptor management appears only where the server enabled it — the
  // scripts run as code on the gateway host.
  const interceptorResource = features.interceptor_editing
    ? [
        {
          name: "interceptors",
          list: "/interceptors",
          meta: { label: "Interceptors", icon: <CodeOutlined /> },
        },
      ]
    : [];

  return (
    <BrowserRouter>
      <ConfigProvider
        theme={{
          token: {
            colorPrimary: "#0f766e",
            colorInfo: "#0f766e",
            colorSuccess: "#16805f",
            colorWarning: "#b7791f",
            colorError: "#c2413b",
            colorText: "#17201e",
            colorTextSecondary: "#62706c",
            colorBorder: "#dfe6e2",
            colorBgBase: "#f4f7f5",
            colorBgContainer: "#ffffff",
            colorBgElevated: "#ffffff",
            borderRadius: 8,
            borderRadiusLG: 12,
            controlHeight: 40,
            fontFamily:
              "Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif",
            boxShadowSecondary: "0 18px 50px rgba(24, 45, 39, 0.14)",
          },
          components: {
            Button: {
              fontWeight: 600,
              primaryShadow: "0 1px 2px rgba(15, 118, 110, 0.16)",
            },
            Table: {
              headerBg: "#e8efec",
              headerColor: "#31413d",
              borderColor: "#d3ddd8",
              rowHoverBg: "#edf7f4",
              cellPaddingBlockSM: 14,
              cellPaddingInlineSM: 16,
            },
            Drawer: {
              colorBgElevated: "#fbfcfb",
            },
          },
        }}
      >
        <AntdApp>
          <Refine
            dataProvider={dataProvider(API_URL, httpClient)}
            authProvider={authProvider}
            routerProvider={routerProvider}
            notificationProvider={useNotificationProvider}
            resources={[
              {
                name: "dashboard",
                list: "/",
                meta: { label: "Dashboard", icon: <DashboardOutlined /> },
              },
              {
                name: "connectors",
                list: "/connectors",
                meta: { label: "Connectors", icon: <ApiOutlined /> },
              },
              {
                name: "routes",
                list: "/routes",
                meta: { label: "MT Routes", icon: <ShareAltOutlined /> },
              },
              {
                name: "mo-routes",
                list: "/mo-routes",
                meta: { label: "MO Routes", icon: <InboxOutlined /> },
              },
              {
                name: "filters",
                list: "/filters",
                meta: { label: "Saved Filters", icon: <FilterOutlined /> },
              },
              {
                name: "http-connectors",
                list: "/http-connectors",
                meta: { label: "HTTP Destinations", icon: <GlobalOutlined /> },
              },
              ...interceptorResource,
              {
                name: "groups",
                list: "/groups",
                meta: { label: "Groups", icon: <TeamOutlined /> },
              },
              {
                name: "users",
                list: "/users",
                meta: { label: "Users", icon: <UserOutlined /> },
              },
              {
                name: "smpps-users",
                list: "/smpps-users",
                meta: { label: "SMPPs Binds", icon: <LinkOutlined /> },
              },
              {
                name: "partner-onboarding",
                list: "/partners/onboarding",
                meta: { label: "Partner Onboarding", icon: <UsergroupAddOutlined /> },
              },
              {
                name: "operations",
                list: "/operations",
                meta: { label: "Operations", icon: <FundProjectionScreenOutlined /> },
              },
              {
                name: "profiles",
                list: "/profiles",
                meta: { label: "Profiles", icon: <SafetyCertificateOutlined /> },
              },
              {
                name: "education",
                list: "/learn",
                meta: { label: "Education Center", icon: <BookOutlined /> },
              },
            ]}
            // disableTelemetry: Refine otherwise POSTs to telemetry.refine.dev
            // on mount. This console administers an SMS gateway and is expected
            // to run in air-gapped and regulated deployments, so it must make no
            // outbound third-party call.
            options={{
              syncWithLocation: true,
              warnWhenUnsavedChanges: true,
              disableTelemetry: true,
            }}
          >
            <Routes>
              <Route
                element={
                  <Authenticated key="authed" fallback={<CatchAllNavigate to="/login" />}>
                    <AppShell showInterceptors={features.interceptor_editing}>
                      <Suspense fallback={<PageLoading />}>
                        <Outlet />
                      </Suspense>
                    </AppShell>
                  </Authenticated>
                }
              >
                <Route index element={<DashboardPage />} />
                <Route path="/connectors" element={<ConnectorList />} />
                <Route path="/routes" element={<RouteList />} />
                <Route path="/mo-routes" element={<MORouteList />} />
                <Route path="/filters" element={<FilterListPage />} />
                <Route path="/http-connectors" element={<HTTPConnectorList />} />
                {features.interceptor_editing && (
                  <Route path="/interceptors" element={<InterceptorList />} />
                )}
                <Route path="/groups" element={<GroupList />} />
                <Route path="/users" element={<UserList />} />
                <Route path="/smpps-users" element={<SMPPsUserList />} />
                <Route path="/partners/onboarding" element={<PartnerOnboardingPage />} />
                <Route path="/operations" element={<OperationsPage />} />
                <Route path="/profiles" element={<ProfilesPage />} />
                <Route path="/learn" element={<EducationPage />} />
                <Route path="*" element={<ErrorComponent />} />
              </Route>
              <Route
                element={
                  <Authenticated key="auth-pages" fallback={<Outlet />}>
                    <NavigateToResource resource="dashboard" />
                  </Authenticated>
                }
              >
                <Route
                  path="/login"
                  element={
                    <Suspense fallback={<PageLoading />}>
                      <LoginPage />
                    </Suspense>
                  }
                />
              </Route>
            </Routes>
            <UnsavedChangesNotifier />
            <DocumentTitleHandler
              handler={({ resource }) => {
                const label = String(resource?.meta?.label || resource?.name || "").trim();
                return label ? `${label} · Synevyr` : "Synevyr Messaging Platform";
              }}
            />
          </Refine>
        </AntdApp>
      </ConfigProvider>
    </BrowserRouter>
  );
}
