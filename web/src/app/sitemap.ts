import type { MetadataRoute } from "next";
import { PUBLIC_ROUTES } from "@/lib/publicRoutes";
import { siteUrl } from "@/lib/site";

/**
 * Exactly the public routes, derived from the same list AuthGate and Shell use
 * so the three cannot drift.
 *
 * /login is excluded even though it is technically public: it is an operator
 * door, there is no signup on a published deployment, and listing it invites
 * credential-stuffing traffic for no benefit.
 *
 * A sitemap is also a disclosure surface. Enumerating private routes here
 * would hand an index of the whole application to anyone who asks for a file
 * that exists precisely so it can be fetched anonymously.
 */
export default function sitemap(): MetadataRoute.Sitemap {
  const base = siteUrl();
  return PUBLIC_ROUTES.filter((r) => r !== "/login").map((route) => ({
    url: route === "/" ? base : `${base}${route}`,
    lastModified: new Date(),
    changeFrequency: "daily" as const,
    priority: route === "/" ? 1 : 0.7,
  }));
}
