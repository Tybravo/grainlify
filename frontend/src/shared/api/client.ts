/**
 * API Client for Patchwork Backend
 * Base URL: http://7nonainmv1.loclx.io
 */

import { API_BASE_URL } from '../config/api';

// Token management
export const getAuthToken = (): string | null => {
  return localStorage.getItem('patchwork_jwt');
};

export const setAuthToken = (token: string): void => {
  localStorage.setItem('patchwork_jwt', token);
};

export const removeAuthToken = (): void => {
  localStorage.removeItem('patchwork_jwt');
};

// API request helper
interface ApiRequestOptions extends RequestInit {
  requiresAuth?: boolean;
}

async function apiRequest<T>(
  endpoint: string,
  options: ApiRequestOptions = {}
): Promise<T> {
  const { requiresAuth = false, headers = {}, ...fetchOptions } = options;

  const url = `${API_BASE_URL}${endpoint}`;
  const requestHeaders: HeadersInit = {
    'Content-Type': 'application/json',
    ...headers,
  };

  // Add auth token if required
  if (requiresAuth) {
    const token = getAuthToken();
    if (token) {
      requestHeaders['Authorization'] = `Bearer ${token}`;
    }
  }

  let response: Response;
  try {
    response = await fetch(url, {
      ...fetchOptions,
      headers: requestHeaders,
    });
  } catch (err) {
    // Network error (CORS, connection refused, etc.)
    if (err instanceof TypeError && err.message.includes('fetch')) {
      throw new Error('Network error: Unable to connect to the server. Please check your connection.');
    }
    throw err;
  }

  // Handle errors
  if (!response.ok) {
    if (response.status === 401) {
      // Token expired or invalid - clear it
      removeAuthToken();
      throw new Error('Authentication failed. Please sign in again.');
    }

    // Try to parse error response
    try {
      const errorData = await response.json();
      throw new Error(errorData.message || errorData.error || 'API request failed');
    } catch {
      throw new Error(`API request failed with status ${response.status}`);
    }
  }

  // Parse JSON response
  try {
    return await response.json();
  } catch (err) {
    // If response is empty or not JSON, return empty array for list endpoints
    if (endpoint.includes('/projects/mine') || endpoint.includes('/projects')) {
      return [] as T;
    }
    throw new Error('Invalid response from server');
  }
}

// API Methods

// Health & Status
export const checkHealth = () => 
  apiRequest<{ ok: boolean; service: string }>('/health');

export const checkReady = () => 
  apiRequest<{ ok: boolean; db: string }>('/ready');

// Authentication
export const getCurrentUser = () =>
  apiRequest<{ 
    id: string; 
    role: string;
    github: {
      login: string;
      avatar_url: string;
    };
  }>('/me', { requiresAuth: true });

export const getGitHubLoginUrl = () => {
  return `${API_BASE_URL}/auth/github/login/start`;
};

export const getGitHubStatus = () =>
  apiRequest<{ 
    linked: boolean; 
    github?: { id: number; login: string } 
  }>('/auth/github/status', { requiresAuth: true });

// User Profile
export const getUserProfile = () =>
  apiRequest<{
    contributions_count: number;
    projects_contributed_to_count: number;
    projects_led_count: number;
    rewards_count: number;
    languages: Array<{ language: string; contribution_count: number }>;
    ecosystems: Array<{ ecosystem_name: string; contribution_count: number }>;
    rank: {
      position: number | null;
      tier: string;
      tier_name: string;
      tier_color: string;
    };
  }>('/profile', { requiresAuth: true });

export const getProfileCalendar = () =>
  apiRequest<{
    calendar: Array<{ date: string; count: number; level: number }>;
    total: number;
  }>('/profile/calendar', { requiresAuth: true });

export const getProfileActivity = (limit = 50, offset = 0) =>
  apiRequest<{
    activities: Array<{
      type: 'pull_request' | 'issue';
      id: string;
      number: number;
      title: string;
      url: string;
      date: string;
      month_year: string;
      project_name: string;
      project_id: string;
    }>;
    total: number;
    limit: number;
    offset: number;
  }>(`/profile/activity?limit=${limit}&offset=${offset}`, { requiresAuth: true });

// Projects
export const getPublicProjects = (params?: {
  ecosystem?: string;
  language?: string;
  category?: string;
  tags?: string;
  limit?: number;
  offset?: number;
}) => {
  const queryParams = new URLSearchParams();
  if (params?.ecosystem) queryParams.append('ecosystem', params.ecosystem);
  if (params?.language) queryParams.append('language', params.language);
  if (params?.category) queryParams.append('category', params.category);
  if (params?.tags) queryParams.append('tags', params.tags);
  if (params?.limit) queryParams.append('limit', params.limit.toString());
  if (params?.offset) queryParams.append('offset', params.offset.toString());

  const queryString = queryParams.toString();
  const endpoint = queryString ? `/projects?${queryString}` : '/projects';

  return apiRequest<{
    projects: Array<{
      id: string;
      github_full_name: string;
      language: string | null;
      tags: string[];
      category: string | null;
      stars_count: number;
      forks_count: number;
      contributors_count: number;
      open_issues_count: number;
      open_prs_count: number;
      ecosystem_name: string | null;
      ecosystem_slug: string | null;
      description?: string;
      created_at: string;
      updated_at: string;
    }>;
    total: number;
    limit: number;
    offset: number;
  }>(endpoint);
};

// Get recommended projects (top by contributors count)
export const getRecommendedProjects = (limit: number = 8) =>
  apiRequest<{
    projects: Array<{
      id: string;
      github_full_name: string;
      language: string | null;
      tags: string[];
      category: string | null;
      stars_count: number;
      forks_count: number;
      contributors_count: number;
      open_issues_count: number;
      open_prs_count: number;
      ecosystem_name: string | null;
      ecosystem_slug: string | null;
      description?: string;
      created_at: string;
      updated_at: string;
    }>;
  }>(`/projects/recommended?limit=${limit}`);

export const getPublicProject = (projectId: string) =>
  apiRequest<{
    id: string;
    github_full_name: string;
    language: string | null;
    tags: string[];
    category: string | null;
    stars_count: number;
    forks_count: number;
    contributors_count: number;
    open_issues_count: number;
    open_prs_count: number;
    ecosystem_name: string | null;
    ecosystem_slug: string | null;
    created_at: string;
    updated_at: string;
    languages: Array<{ name: string; percentage: number }>;
    readme?: string;
    repo?: {
      full_name: string;
      html_url: string;
      homepage: string;
      description: string;
      open_issues_count: number;
      owner_login: string;
      owner_avatar_url: string;
    };
  }>(`/projects/${projectId}`);

export const getPublicProjectIssues = (projectId: string) =>
  apiRequest<{
    issues: Array<{
      github_issue_id: number;
      number: number;
      state: string;
      title: string;
      description: string | null;
      author_login: string;
      labels: any[];
      url: string;
      updated_at: string | null;
      last_seen_at: string;
    }>;
  }>(`/projects/${projectId}/issues/public`);

export const getPublicProjectPRs = (projectId: string) =>
  apiRequest<{
    prs: Array<{
      github_pr_id: number;
      number: number;
      state: string;
      title: string;
      author_login: string;
      url: string;
      merged: boolean;
      created_at: string | null;
      updated_at: string | null;
      closed_at: string | null;
      merged_at: string | null;
      last_seen_at: string;
    }>;
  }>(`/projects/${projectId}/prs/public`);

export const getProjectFilters = () =>
  apiRequest<{
    languages: string[];
    categories: string[];
    tags: string[];
  }>('/projects/filters');

// Ecosystems
export const getEcosystems = () =>
  apiRequest<{
    ecosystems: Array<{
      id: string;
      slug: string;
      name: string;
      description: string;
      website_url: string;
      status: string;
      project_count: number;
      user_count: number;
      created_at: string;
      updated_at: string;
    }>;
  }>('/ecosystems');

// Leaderboard
export const getLeaderboard = (limit = 10) =>
  apiRequest<Array<{
    rank: number;
    rank_tier: string;
    rank_tier_name: string;
    username: string;
    avatar: string;
    user_id: string;
    contributions: number;
    ecosystems: string[];
    score: number;
    trend: 'up' | 'down' | 'same';
    trendValue: number;
  }>>(`/leaderboard?limit=${limit}`);

// KYC
export const startKYCVerification = () =>
  apiRequest<{
    session_id: string;
    url: string;
  }>('/auth/kyc/start', { 
    requiresAuth: true,
    method: 'POST' 
  });

export const getKYCStatus = () =>
  apiRequest<{
    status: string | null;
    session_id?: string;
    verified_at?: string;
    rejection_reason?: string;
    data?: any;
    extracted?: any;
  }>('/auth/kyc/status', { requiresAuth: true });

// My Projects (for maintainers)
export const getMyProjects = () =>
  apiRequest<Array<{
    id: string;
    github_full_name: string;
    github_repo_id: number;
    status: string;
    ecosystem_name: string;
    language: string;
    tags: string[];
    category: string;
    verification_error: string | null;
    verified_at: string | null;
    webhook_created_at: string | null;
    webhook_id: number | null;
    webhook_url: string | null;
    created_at: string;
    updated_at: string;
  }>>('/projects/mine', { requiresAuth: true });

export const createProject = (data: {
  github_full_name: string;
  ecosystem_name: string;
  language?: string;
  tags?: string[];
  category?: string;
}) =>
  apiRequest<{
    id: string;
    github_full_name: string;
    status: string;
    ecosystem_name: string;
    language: string;
    tags: string[];
    category: string;
    created_at: string;
    updated_at: string;
  }>('/projects', {
    requiresAuth: true,
    method: 'POST',
    body: JSON.stringify(data),
  });

export const verifyProject = (projectId: string) =>
  apiRequest<{
    id: string;
    status: string;
    verified_at: string;
    webhook_id: number;
    webhook_url: string;
  }>(`/projects/${projectId}/verify`, {
    requiresAuth: true,
    method: 'POST',
  });

export const syncProject = (projectId: string) =>
  apiRequest<{
    ok: boolean;
    message: string;
  }>(`/projects/${projectId}/sync`, {
    requiresAuth: true,
    method: 'POST',
  });

// Project Data (Issues and PRs)
export const getProjectIssues = (projectId: string) =>
  apiRequest<{
    issues: Array<{
      github_issue_id: number;
      number: number;
      state: string;
      title: string;
      description: string | null;
      author_login: string;
      assignees: any[];
      labels: any[];
      comments_count: number;
      comments: any[];
      url: string;
      updated_at: string | null;
      last_seen_at: string;
    }>;
  }>(`/projects/${projectId}/issues`, { requiresAuth: true });

export const getProjectPRs = (projectId: string) =>
  apiRequest<{
    prs: Array<{
      github_pr_id: number;
      number: number;
      state: string;
      title: string;
      author_login: string;
      url: string;
      merged: boolean;
      created_at: string | null;
      updated_at: string | null;
      closed_at: string | null;
      merged_at: string | null;
      last_seen_at: string;
    }>;
  }>(`/projects/${projectId}/prs`, { requiresAuth: true });