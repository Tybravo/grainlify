package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jagadeesh/grainlify/backend/internal/db"
	"github.com/jagadeesh/grainlify/backend/internal/github"
)

type ProjectsPublicHandler struct {
	db *db.DB
}

func NewProjectsPublicHandler(d *db.DB) *ProjectsPublicHandler {
	return &ProjectsPublicHandler{db: d}
}

// Get returns a single verified project by id, enriched with GitHub repo metadata and language breakdown.
func (h *ProjectsPublicHandler) Get() fiber.Handler {
	return func(c *fiber.Ctx) error {
		projectIDParam := c.Params("id")
		slog.Info("projects/:id: handler called",
			"method", c.Method(),
			"path", c.Path(),
			"id_param", projectIDParam,
			"request_id", c.Locals("requestid"),
		)

		if h.db == nil || h.db.Pool == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "db_not_configured"})
		}

		projectID, err := uuid.Parse(projectIDParam)
		if err != nil {
			slog.Warn("projects/:id: invalid project ID format",
				"id_param", projectIDParam,
				"error", err,
				"request_id", c.Locals("requestid"),
			)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_project_id"})
		}

		// Load project from DB (verified + not deleted)
		var id uuid.UUID
		var fullName string
		var language, category *string
		var tagsJSON []byte
		var starsCount, forksCount *int
		var openIssuesCount, openPRsCount, contributorsCount int
		var createdAt, updatedAt time.Time
		var ecosystemName, ecosystemSlug *string

		err = h.db.Pool.QueryRow(c.Context(), `
SELECT 
  p.id,
  p.github_full_name,
  p.language,
  p.tags,
  p.category,
  p.stars_count,
  p.forks_count,
  (
    SELECT COUNT(*)
    FROM github_issues gi
    WHERE gi.project_id = p.id AND gi.state = 'open'
  ) AS open_issues_count,
  (
    SELECT COUNT(*)
    FROM github_pull_requests gpr
    WHERE gpr.project_id = p.id AND gpr.state = 'open'
  ) AS open_prs_count,
  (
    SELECT COUNT(DISTINCT a.author_login)
    FROM (
      SELECT author_login FROM github_issues WHERE project_id = p.id AND author_login IS NOT NULL AND author_login != ''
      UNION
      SELECT author_login FROM github_pull_requests WHERE project_id = p.id AND author_login IS NOT NULL AND author_login != ''
    ) a
  ) AS contributors_count,
  p.created_at,
  p.updated_at,
  e.name AS ecosystem_name,
  e.slug AS ecosystem_slug
FROM projects p
LEFT JOIN ecosystems e ON p.ecosystem_id = e.id
WHERE p.id = $1 AND p.status = 'verified' AND p.deleted_at IS NULL
`, projectID).Scan(
			&id, &fullName, &language, &tagsJSON, &category, &starsCount, &forksCount,
			&openIssuesCount, &openPRsCount, &contributorsCount,
			&createdAt, &updatedAt, &ecosystemName, &ecosystemSlug,
		)
		if err == pgx.ErrNoRows {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "project_not_found"})
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "project_lookup_failed"})
		}

		// Parse tags JSONB
		var tags []string
		if len(tagsJSON) > 0 {
			_ = json.Unmarshal(tagsJSON, &tags)
		}

		// Default stars/forks to 0 if nil
		stars := 0
		if starsCount != nil {
			stars = *starsCount
		}
		forks := 0
		if forksCount != nil {
			forks = *forksCount
		}

		// Enrich from GitHub (best effort, public/no-auth)
		ctx, cancel := context.WithTimeout(c.Context(), 6*time.Second)
		defer cancel()
		gh := github.NewClient()

		var repo github.Repo
		repoOK := false
		if r, err := gh.GetRepo(ctx, "", fullName); err == nil {
			repo = r
			repoOK = true
			// Prefer live counts from GitHub if available
			stars = repo.StargazersCount
			forks = repo.ForksCount
			// Best-effort persist
			_, _ = h.db.Pool.Exec(c.Context(), `
UPDATE projects SET stars_count=$2, forks_count=$3, updated_at=now()
WHERE id=$1
`, projectID, stars, forks)
		} else {
			slog.Warn("failed to fetch repo metadata from GitHub",
				"project_id", projectID,
				"github_full_name", fullName,
				"error", err,
			)
		}

		// GitHub language breakdown (best effort)
		var langsOut []fiber.Map
		if m, err := gh.GetRepoLanguages(ctx, "", fullName); err == nil && len(m) > 0 {
			var total int64
			for _, v := range m {
				total += v
			}
			if total > 0 {
				for name, v := range m {
					pct := float64(v) * 100.0 / float64(total)
					langsOut = append(langsOut, fiber.Map{
						"name":       name,
						"percentage": pct,
					})
				}
			}
		}

		// Fetch README content (best effort)
		var readmeContent string
		if readme, err := gh.GetReadme(ctx, "", fullName); err == nil {
			readmeContent = readme
		} else {
			slog.Warn("failed to fetch README for project",
				"project_id", projectID,
				"github_full_name", fullName,
				"error", err,
			)
		}

		resp := fiber.Map{
			"id":                 id.String(),
			"github_full_name":   fullName,
			"language":           language,
			"tags":               tags,
			"category":           category,
			"stars_count":        stars,
			"forks_count":        forks,
			"contributors_count": contributorsCount,
			"open_issues_count":  openIssuesCount,
			"open_prs_count":     openPRsCount,
			"ecosystem_name":     ecosystemName,
			"ecosystem_slug":     ecosystemSlug,
			"created_at":         createdAt,
			"updated_at":         updatedAt,
			"languages":          langsOut,
			"readme":             readmeContent,
		}

		if repoOK {
			resp["repo"] = fiber.Map{
				"full_name":         repo.FullName,
				"html_url":          repo.HTMLURL,
				"homepage":          repo.Homepage,
				"description":       repo.Description,
				"open_issues_count": repo.OpenIssuesCount,
				"owner_login":       repo.Owner.Login,
				"owner_avatar_url":  repo.Owner.AvatarURL,
			}
		}

		return c.Status(fiber.StatusOK).JSON(resp)
	}
}

// IssuesPublic returns recent issues for a verified project (read-only, no auth).
func (h *ProjectsPublicHandler) IssuesPublic() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if h.db == nil || h.db.Pool == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "db_not_configured"})
		}
		projectID, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_project_id"})
		}

		// Ensure project is verified and not deleted
		var ok bool
		if err := h.db.Pool.QueryRow(c.Context(), `
SELECT EXISTS(
  SELECT 1 FROM projects WHERE id=$1 AND status='verified' AND deleted_at IS NULL
)
`, projectID).Scan(&ok); err != nil || !ok {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "project_not_found"})
		}

		rows, err := h.db.Pool.Query(c.Context(), `
SELECT github_issue_id, number, state, title, body, author_login, url, labels, updated_at_github, last_seen_at
FROM github_issues
WHERE project_id = $1
ORDER BY COALESCE(updated_at_github, last_seen_at) DESC
LIMIT 50
`, projectID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "issues_list_failed"})
		}
		defer rows.Close()

		var out []fiber.Map
		for rows.Next() {
			var gid int64
			var number int
			var state, title, author, url string
			var body *string
			var labelsJSON []byte
			var updated *time.Time
			var lastSeen time.Time
			if err := rows.Scan(&gid, &number, &state, &title, &body, &author, &url, &labelsJSON, &updated, &lastSeen); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "issues_list_failed"})
			}

			// labels JSONB (stored as array of objects) -> surface as-is
			var labels []any
			if len(labelsJSON) > 0 {
				_ = json.Unmarshal(labelsJSON, &labels)
			}

			out = append(out, fiber.Map{
				"github_issue_id": gid,
				"number":          number,
				"state":           state,
				"title":           title,
				"description":     body,
				"author_login":    author,
				"labels":          labels,
				"url":             url,
				"updated_at":      updated,
				"last_seen_at":    lastSeen,
			})
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{"issues": out})
	}
}

// PRsPublic returns recent PRs for a verified project (read-only, no auth).
func (h *ProjectsPublicHandler) PRsPublic() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if h.db == nil || h.db.Pool == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "db_not_configured"})
		}
		projectID, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_project_id"})
		}

		var ok bool
		if err := h.db.Pool.QueryRow(c.Context(), `
SELECT EXISTS(
  SELECT 1 FROM projects WHERE id=$1 AND status='verified' AND deleted_at IS NULL
)
`, projectID).Scan(&ok); err != nil || !ok {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "project_not_found"})
		}

		rows, err := h.db.Pool.Query(c.Context(), `
SELECT github_pr_id, number, state, title, author_login, url, merged, 
       created_at_github, updated_at_github, closed_at_github, merged_at_github, last_seen_at
FROM github_pull_requests
WHERE project_id = $1
ORDER BY COALESCE(updated_at_github, last_seen_at) DESC
LIMIT 50
`, projectID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "prs_list_failed"})
		}
		defer rows.Close()

		var out []fiber.Map
		for rows.Next() {
			var gid int64
			var number int
			var state, title, author, url string
			var merged bool
			var createdAt, updated, closedAt, mergedAt *time.Time
			var lastSeen time.Time
			if err := rows.Scan(&gid, &number, &state, &title, &author, &url, &merged, &createdAt, &updated, &closedAt, &mergedAt, &lastSeen); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "prs_list_failed"})
			}
			out = append(out, fiber.Map{
				"github_pr_id": gid,
				"number":       number,
				"state":        state,
				"title":        title,
				"author_login": author,
				"url":          url,
				"merged":       merged,
				"created_at":   createdAt,
				"updated_at":   updated,
				"closed_at":    closedAt,
				"merged_at":    mergedAt,
				"last_seen_at": lastSeen,
			})
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{"prs": out})
	}
}

// List returns a filtered list of verified projects.
// Query parameters:
//   - ecosystem: filter by ecosystem name (case-insensitive)
//   - language: filter by programming language
//   - category: filter by category
//   - tags: comma-separated list of tags (project must have ALL tags)
//   - limit: max results (default 50, max 200)
//   - offset: pagination offset (default 0)
func (h *ProjectsPublicHandler) List() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if h.db == nil || h.db.Pool == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "db_not_configured"})
		}

		// Parse query parameters
		ecosystem := strings.TrimSpace(c.Query("ecosystem"))
		language := strings.TrimSpace(c.Query("language"))
		category := strings.TrimSpace(c.Query("category"))
		tagsParam := strings.TrimSpace(c.Query("tags"))
		
		limit := 50
		if l := c.QueryInt("limit", 50); l > 0 && l <= 200 {
			limit = l
		}
		offset := c.QueryInt("offset", 0)
		if offset < 0 {
			offset = 0
		}

		// Build WHERE clause and args
		var conditions []string
		var args []any
		argPos := 1

		// Only show verified projects
		conditions = append(conditions, "p.status = 'verified'")

		// Filter by ecosystem
		if ecosystem != "" {
			conditions = append(conditions, fmt.Sprintf("LOWER(TRIM(e.name)) = LOWER($%d)", argPos))
			args = append(args, ecosystem)
			argPos++
		}

		// Filter by language
		if language != "" {
			conditions = append(conditions, fmt.Sprintf("LOWER(TRIM(p.language)) = LOWER($%d)", argPos))
			args = append(args, language)
			argPos++
		}

		// Filter by category
		if category != "" {
			conditions = append(conditions, fmt.Sprintf("LOWER(TRIM(p.category)) = LOWER($%d)", argPos))
			args = append(args, category)
			argPos++
		}

		// Filter by tags (must have ALL specified tags)
		var tags []string
		if tagsParam != "" {
			for _, tag := range strings.Split(tagsParam, ",") {
				tag = strings.TrimSpace(tag)
				if tag != "" {
					tags = append(tags, tag)
				}
			}
		}
		if len(tags) > 0 {
			// Use JSONB containment operator @> to check if tags array contains all specified tags
			conditions = append(conditions, fmt.Sprintf("p.tags @> $%d::jsonb", argPos))
			tagsJSON, _ := json.Marshal(tags)
			args = append(args, string(tagsJSON))
			argPos++
		}

		whereClause := strings.Join(conditions, " AND ")

		// Build query
		query := fmt.Sprintf(`
SELECT 
  p.id,
  p.github_full_name,
  p.language,
  p.tags,
  p.category,
  p.stars_count,
  p.forks_count,
  (
    SELECT COUNT(*)
    FROM github_issues gi
    WHERE gi.project_id = p.id AND gi.state = 'open'
  ) AS open_issues_count,
  (
    SELECT COUNT(*)
    FROM github_pull_requests gpr
    WHERE gpr.project_id = p.id AND gpr.state = 'open'
  ) AS open_prs_count,
  (
    SELECT COUNT(DISTINCT a.author_login)
    FROM (
      SELECT author_login FROM github_issues WHERE project_id = p.id AND author_login IS NOT NULL AND author_login != ''
      UNION
      SELECT author_login FROM github_pull_requests WHERE project_id = p.id AND author_login IS NOT NULL AND author_login != ''
    ) a
  ) AS contributors_count,
  p.created_at,
  p.updated_at,
  e.name AS ecosystem_name,
  e.slug AS ecosystem_slug
FROM projects p
LEFT JOIN ecosystems e ON p.ecosystem_id = e.id
WHERE %s
ORDER BY p.created_at DESC
LIMIT $%d OFFSET $%d
`, whereClause, argPos, argPos+1)
		args = append(args, limit, offset)

		rows, err := h.db.Pool.Query(c.Context(), query, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "projects_list_failed"})
		}
		defer rows.Close()

		// Enrich with GitHub data (best effort, in background)
		ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
		defer cancel()
		gh := github.NewClient()

		var out []fiber.Map
		for rows.Next() {
			var id uuid.UUID
			var fullName string
			var language, category *string
			var tagsJSON []byte
			var starsCount, forksCount *int
			var openIssuesCount, openPRsCount, contributorsCount int
			var createdAt, updatedAt time.Time
			var ecosystemName, ecosystemSlug *string

			if err := rows.Scan(&id, &fullName, &language, &tagsJSON, &category, &starsCount, &forksCount, &openIssuesCount, &openPRsCount, &contributorsCount, &createdAt, &updatedAt, &ecosystemName, &ecosystemSlug); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "projects_list_failed", "details": err.Error()})
			}

			// Parse tags JSONB
			var tags []string
			if len(tagsJSON) > 0 {
				_ = json.Unmarshal(tagsJSON, &tags)
			}

			// Default to 0 if nil
			stars := 0
			if starsCount != nil {
				stars = *starsCount
			}
			forks := 0
			if forksCount != nil {
				forks = *forksCount
			}

			// Get repo description from GitHub (best effort)
			var description string
			if repo, err := gh.GetRepo(ctx, "", fullName); err == nil {
				description = repo.Description
				// If stars or forks are 0, update them from GitHub
				if stars == 0 {
					stars = repo.StargazersCount
				}
				if forks == 0 {
					forks = repo.ForksCount
				}
				// Best-effort persist (non-blocking)
				if stars > 0 || forks > 0 {
					go func(projectID uuid.UUID, st, fk int) {
						_, _ = h.db.Pool.Exec(context.Background(), `
UPDATE projects SET stars_count=$2, forks_count=$3, updated_at=now()
WHERE id=$1
`, projectID, st, fk)
					}(id, stars, forks)
				}
			} else {
				// If GitHub fetch fails, try to fetch fresh data in background
				if stars == 0 || forks == 0 {
					go func(projectID uuid.UUID, projectFullName string) {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						gh := github.NewClient()
						if r, err := gh.GetRepo(ctx, "", projectFullName); err == nil {
							slog.Info("fetched fresh stars/forks from GitHub for project", "project_id", projectID, "full_name", projectFullName, "stars", r.StargazersCount, "forks", r.ForksCount)
							_, err := h.db.Pool.Exec(ctx, `
UPDATE projects SET stars_count=$2, forks_count=$3, updated_at=now()
WHERE id=$1
`, projectID, r.StargazersCount, r.ForksCount)
							if err != nil {
								slog.Warn("failed to persist fresh stars/forks for project", "project_id", projectID, "error", err)
							}
						} else {
							slog.Warn("failed to fetch fresh stars/forks from GitHub for project", "project_id", projectID, "full_name", projectFullName, "error", err)
						}
					}(id, fullName)
				}
			}

			out = append(out, fiber.Map{
				"id":              id.String(),
				"github_full_name": fullName,
				"language":        language,
				"tags":            tags,
				"category":        category,
				"stars_count":     stars,
				"forks_count":     forks,
				"contributors_count": contributorsCount,
				"open_issues_count":  openIssuesCount,
				"open_prs_count":     openPRsCount,
				"ecosystem_name":  ecosystemName,
				"ecosystem_slug":  ecosystemSlug,
				"description":     description,
				"created_at":      createdAt,
				"updated_at":      updatedAt,
			})
		}

		// Get total count for pagination
		countQuery := fmt.Sprintf(`
SELECT COUNT(*)
FROM projects p
LEFT JOIN ecosystems e ON p.ecosystem_id = e.id
WHERE %s
`, whereClause)
		countArgs := args[:len(args)-2] // Remove limit and offset

		var total int
		if err := h.db.Pool.QueryRow(c.Context(), countQuery, countArgs...).Scan(&total); err != nil {
			// If count fails, just return results without total
			total = len(out)
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"projects": out,
			"total":    total,
			"limit":    limit,
			"offset":   offset,
		})
	}
}

// Recommended returns top projects ordered by contributors count, enriched with GitHub data.
// Query parameters:
//   - limit: max results (default 8, max 20)
func (h *ProjectsPublicHandler) Recommended() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if h.db == nil || h.db.Pool == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "db_not_configured"})
		}

		limit := 8
		if l := c.QueryInt("limit", 8); l > 0 && l <= 20 {
			limit = l
		}

		// Query top projects by contributors count
		query := `
SELECT 
  p.id,
  p.github_full_name,
  p.language,
  p.tags,
  p.category,
  p.stars_count,
  p.forks_count,
  (
    SELECT COUNT(*)
    FROM github_issues gi
    WHERE gi.project_id = p.id AND gi.state = 'open'
  ) AS open_issues_count,
  (
    SELECT COUNT(*)
    FROM github_pull_requests gpr
    WHERE gpr.project_id = p.id AND gpr.state = 'open'
  ) AS open_prs_count,
  (
    SELECT COUNT(DISTINCT a.author_login)
    FROM (
      SELECT author_login FROM github_issues WHERE project_id = p.id AND author_login IS NOT NULL AND author_login != ''
      UNION
      SELECT author_login FROM github_pull_requests WHERE project_id = p.id AND author_login IS NOT NULL AND author_login != ''
    ) a
  ) AS contributors_count,
  p.created_at,
  p.updated_at,
  e.name AS ecosystem_name,
  e.slug AS ecosystem_slug
FROM projects p
LEFT JOIN ecosystems e ON p.ecosystem_id = e.id
WHERE p.status = 'verified' AND p.deleted_at IS NULL
ORDER BY contributors_count DESC, p.stars_count DESC, p.created_at DESC
LIMIT $1
`
		rows, err := h.db.Pool.Query(c.Context(), query, limit)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "recommended_projects_failed"})
		}
		defer rows.Close()

		// Enrich with GitHub data (best effort)
		ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
		defer cancel()
		gh := github.NewClient()

		var out []fiber.Map
		for rows.Next() {
			var id uuid.UUID
			var fullName string
			var language, category *string
			var tagsJSON []byte
			var starsCount, forksCount *int
			var openIssuesCount, openPRsCount, contributorsCount int
			var createdAt, updatedAt time.Time
			var ecosystemName, ecosystemSlug *string

			if err := rows.Scan(&id, &fullName, &language, &tagsJSON, &category, &starsCount, &forksCount, &openIssuesCount, &openPRsCount, &contributorsCount, &createdAt, &updatedAt, &ecosystemName, &ecosystemSlug); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "recommended_projects_scan_failed"})
			}

			// Parse tags JSONB
			var tags []string
			if len(tagsJSON) > 0 {
				_ = json.Unmarshal(tagsJSON, &tags)
			}

			// Default to 0 if nil
			stars := 0
			if starsCount != nil {
				stars = *starsCount
			}
			forks := 0
			if forksCount != nil {
				forks = *forksCount
			}

			// Fetch fresh data from GitHub if stars/forks are 0 or nil
			if stars == 0 || forks == 0 {
				if repo, err := gh.GetRepo(ctx, "", fullName); err == nil {
					if repo.StargazersCount > 0 {
						stars = repo.StargazersCount
					}
					if repo.ForksCount > 0 {
						forks = repo.ForksCount
					}
					// Best-effort persist (non-blocking)
					go func(projectID uuid.UUID, st, fk int) {
						_, _ = h.db.Pool.Exec(context.Background(), `
UPDATE projects SET stars_count=$2, forks_count=$3, updated_at=now()
WHERE id=$1
`, projectID, st, fk)
					}(id, stars, forks)
				}
			}

			// Get repo description from GitHub
			var description string
			if repo, err := gh.GetRepo(ctx, "", fullName); err == nil {
				description = repo.Description
			}

			out = append(out, fiber.Map{
				"id":                 id.String(),
				"github_full_name":   fullName,
				"language":           language,
				"tags":               tags,
				"category":           category,
				"stars_count":        stars,
				"forks_count":        forks,
				"contributors_count": contributorsCount,
				"open_issues_count":  openIssuesCount,
				"open_prs_count":     openPRsCount,
				"ecosystem_name":     ecosystemName,
				"ecosystem_slug":     ecosystemSlug,
				"description":       description,
				"created_at":         createdAt,
				"updated_at":         updatedAt,
			})
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"projects": out,
		})
	}
}

// FilterOptions returns available filter values (languages, categories, tags) from verified projects.
func (h *ProjectsPublicHandler) FilterOptions() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if h.db == nil || h.db.Pool == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "db_not_configured"})
		}

		// Get distinct languages
		langRows, err := h.db.Pool.Query(c.Context(), `
SELECT DISTINCT language
FROM projects
WHERE status = 'verified' AND language IS NOT NULL AND language != ''
ORDER BY language
`)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "filter_options_failed"})
		}
		defer langRows.Close()

		var languages []string
		for langRows.Next() {
			var lang string
			if err := langRows.Scan(&lang); err == nil {
				languages = append(languages, lang)
			}
		}

		// Get distinct categories
		catRows, err := h.db.Pool.Query(c.Context(), `
SELECT DISTINCT category
FROM projects
WHERE status = 'verified' AND category IS NOT NULL AND category != ''
ORDER BY category
`)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "filter_options_failed"})
		}
		defer catRows.Close()

		var categories []string
		for catRows.Next() {
			var cat string
			if err := catRows.Scan(&cat); err == nil {
				categories = append(categories, cat)
			}
		}

		// Get all unique tags from verified projects
		tagRows, err := h.db.Pool.Query(c.Context(), `
SELECT DISTINCT jsonb_array_elements_text(tags) AS tag
FROM projects
WHERE status = 'verified' AND tags IS NOT NULL AND jsonb_array_length(tags) > 0
ORDER BY tag
`)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "filter_options_failed"})
		}
		defer tagRows.Close()

		tagMap := make(map[string]bool)
		for tagRows.Next() {
			var tag string
			if err := tagRows.Scan(&tag); err == nil && tag != "" {
				tagMap[tag] = true
			}
		}
		var tags []string
		for tag := range tagMap {
			tags = append(tags, tag)
		}
		// Sort tags
		for i := 0; i < len(tags)-1; i++ {
			for j := i + 1; j < len(tags); j++ {
				if tags[i] > tags[j] {
					tags[i], tags[j] = tags[j], tags[i]
				}
			}
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"languages": languages,
			"categories": categories,
			"tags":      tags,
		})
	}
}

