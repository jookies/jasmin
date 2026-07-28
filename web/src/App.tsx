import { Refine, Authenticated } from "@refinedev/core";
import {
  ThemedLayoutV2,
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
import { ConfigProvider, App as AntdApp, theme } from "antd";
import dataProvider from "@refinedev/simple-rest";
import {
  ApiOutlined,
  CodeOutlined,
  DashboardOutlined,
  InboxOutlined,
  LinkOutlined,
  ShareAltOutlined,
  UserOutlined,
} from "@ant-design/icons";

import { httpClient, API_URL } from "./httpClient";
import { authProvider } from "./authProvider";
import { ConnectorList, ConnectorCreate, ConnectorEdit } from "./pages/connectors";
import { RouteList, RouteCreate, RouteEdit } from "./pages/routes";
import { MORouteList, MORouteCreate, MORouteEdit } from "./pages/mo-routes";
import { UserList, UserCreate, UserEdit } from "./pages/users";
import { SMPPsUserList, SMPPsUserCreate, SMPPsUserEdit } from "./pages/smpps-users";
import { InterceptorList, InterceptorCreate, InterceptorEdit } from "./pages/interceptors";
import { DashboardPage } from "./pages/dashboard";
import { LoginPage } from "./pages/login";
import { useFeatures } from "./useFeatures";

export default function App() {
  const features = useFeatures();

  // Interceptor management appears only where the server enabled it — the
  // scripts run as code on the gateway host.
  const interceptorResource = features.interceptor_editing
    ? [
        {
          name: "interceptors",
          list: "/interceptors",
          create: "/interceptors/create",
          edit: "/interceptors/edit/:id",
          meta: { label: "Interceptors", icon: <CodeOutlined /> },
        },
      ]
    : [];

  return (
    <BrowserRouter>
      <ConfigProvider 
        theme={{ 
          algorithm: theme.darkAlgorithm, 
          token: { 
            colorPrimary: '#8b5cf6', 
            borderRadius: 12, 
            fontFamily: "'Inter', sans-serif", 
            colorBgBase: 'transparent',
            colorBgContainer: 'rgba(255, 255, 255, 0.05)',
            colorBgElevated: 'rgba(30, 27, 75, 0.8)',
          }
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
                create: "/connectors/create",
                edit: "/connectors/edit/:id",
                meta: { label: "Connectors", icon: <ApiOutlined /> },
              },
              {
                name: "routes",
                list: "/routes",
                create: "/routes/create",
                edit: "/routes/edit/:id",
                meta: { label: "MT Routes", icon: <ShareAltOutlined /> },
              },
              {
                name: "mo-routes",
                list: "/mo-routes",
                create: "/mo-routes/create",
                edit: "/mo-routes/edit/:id",
                meta: { label: "MO Routes", icon: <InboxOutlined /> },
              },
              ...interceptorResource,
              {
                name: "users",
                list: "/users",
                create: "/users/create",
                edit: "/users/edit/:id",
                meta: { label: "Users", icon: <UserOutlined /> },
              },
              {
                name: "smpps-users",
                list: "/smpps-users",
                create: "/smpps-users/create",
                edit: "/smpps-users/edit/:id",
                meta: { label: "SMPPs Binds", icon: <LinkOutlined /> },
              },
            ]}
            options={{ syncWithLocation: true, warnWhenUnsavedChanges: true }}
          >
            <Routes>
              <Route
                element={
                  <Authenticated key="authed" fallback={<CatchAllNavigate to="/login" />}>
                    <ThemedLayoutV2
                      Title={({ collapsed }) => (
                        <div style={{ padding: 12, fontWeight: 700 }}>
                          {collapsed ? "JA" : "Jasmin Admin"}
                        </div>
                      )}
                    >
                      <Outlet />
                    </ThemedLayoutV2>
                  </Authenticated>
                }
              >
                <Route index element={<DashboardPage />} />
                <Route path="/connectors">
                  <Route index element={<ConnectorList />} />
                  <Route path="create" element={<ConnectorCreate />} />
                  <Route path="edit/:id" element={<ConnectorEdit />} />
                </Route>
                <Route path="/routes">
                  <Route index element={<RouteList />} />
                  <Route path="create" element={<RouteCreate />} />
                  <Route path="edit/:id" element={<RouteEdit />} />
                </Route>
                <Route path="/mo-routes">
                  <Route index element={<MORouteList />} />
                  <Route path="create" element={<MORouteCreate />} />
                  <Route path="edit/:id" element={<MORouteEdit />} />
                </Route>
                {features.interceptor_editing && (
                  <Route path="/interceptors">
                    <Route index element={<InterceptorList />} />
                    <Route path="create" element={<InterceptorCreate />} />
                    <Route path="edit/:id" element={<InterceptorEdit />} />
                  </Route>
                )}
                <Route path="/users">
                  <Route index element={<UserList />} />
                  <Route path="create" element={<UserCreate />} />
                  <Route path="edit/:id" element={<UserEdit />} />
                </Route>
                <Route path="/smpps-users">
                  <Route index element={<SMPPsUserList />} />
                  <Route path="create" element={<SMPPsUserCreate />} />
                  <Route path="edit/:id" element={<SMPPsUserEdit />} />
                </Route>
                <Route path="*" element={<ErrorComponent />} />
              </Route>
              <Route
                element={
                  <Authenticated key="auth-pages" fallback={<Outlet />}>
                    <NavigateToResource resource="dashboard" />
                  </Authenticated>
                }
              >
                <Route path="/login" element={<LoginPage />} />
              </Route>
            </Routes>
            <UnsavedChangesNotifier />
            <DocumentTitleHandler />
          </Refine>
        </AntdApp>
      </ConfigProvider>
    </BrowserRouter>
  );
}
