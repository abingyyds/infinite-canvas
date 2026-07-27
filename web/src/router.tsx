import { createBrowserRouter, Navigate, Outlet } from "react-router-dom";

import { AnalyticsTracker } from "@/components/layout/analytics-tracker";
import { SessionInit } from "@/components/layout/session-init";
import UserLayout from "@/layouts/user-layout";
import AdminLayout from "@/pages/admin/layout";
import AdminAssetsPage from "@/pages/admin/assets";
import AdminCreditLogsPage from "@/pages/admin/credit-logs";
import AdminPromptsPage from "@/pages/admin/prompts";
import AdminSettingsPage from "@/pages/admin/settings";
import AdminUsersPage from "@/pages/admin/users";
import AssetsPage from "@/pages/assets";
import CanvasPage from "@/pages/canvas";
import CanvasProjectPage from "@/pages/canvas/project";
import ConfigPage from "@/pages/config";
import HomePage from "@/pages/home";
import ImagePage from "@/pages/image";
import LoginPage from "@/pages/login";
import NotFound from "@/pages/not-found";
import PromptsPage from "@/pages/prompts";
import VideoPage from "@/pages/video";

export const router = createBrowserRouter([
    {
        element: (
            <>
                <SessionInit />
                <Outlet />
            </>
        ),
        children: [
            {
                element: (
                    <UserLayout>
                        <AnalyticsTracker />
                        <Outlet />
                    </UserLayout>
                ),
                children: [
                    { path: "/", element: <HomePage /> },
                    { path: "/image", element: <ImagePage /> },
                    { path: "/video", element: <VideoPage /> },
                    { path: "/assets", element: <AssetsPage /> },
                    { path: "/prompts", element: <PromptsPage /> },
                    { path: "/canvas", element: <CanvasPage /> },
                    { path: "/canvas/:id", element: <CanvasProjectPage /> },
                    { path: "/config", element: <ConfigPage /> },
                ],
            },
            { path: "/login", element: <LoginPage /> },
            {
                path: "/admin",
                element: <AdminLayout />,
                children: [
                    { index: true, element: <Navigate to="/admin/users" replace /> },
                    { path: "users", element: <AdminUsersPage /> },
                    { path: "credit-logs", element: <AdminCreditLogsPage /> },
                    { path: "prompts", element: <AdminPromptsPage /> },
                    { path: "assets", element: <AdminAssetsPage /> },
                    { path: "settings", element: <AdminSettingsPage /> },
                ],
            },
        ],
    },
    { path: "*", element: <NotFound /> },
]);
