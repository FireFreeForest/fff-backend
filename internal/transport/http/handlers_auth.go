package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"firefreeforest/backend/internal/domain"
	"firefreeforest/backend/internal/repository"
	"firefreeforest/backend/internal/service"
)

func (h *Handler) login(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_request", "Email dan password wajib diisi dengan benar")
		return
	}

	pair, user, err := h.auth.Login(c.Request.Context(), req.Email, req.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidCredentials):
			respondError(c, http.StatusUnauthorized, "invalid_credentials", "Email atau password salah")
		case errors.Is(err, service.ErrInactiveUser):
			respondError(c, http.StatusForbidden, "account_inactive", "Akun tidak aktif")
		default:
			h.log.Error("login failed", "error", err)
			respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		}
		return
	}

	respondData(c, http.StatusOK, gin.H{"tokens": pair, "user": user})
}

func (h *Handler) refresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_request", "refresh_token wajib diisi")
		return
	}

	pair, err := h.auth.Refresh(c.Request.Context(), req.RefreshToken, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTokenReuse):
			respondError(c, http.StatusUnauthorized, "token_reuse", "Refresh token sudah digunakan; semua sesi dicabut")
		case errors.Is(err, service.ErrInactiveUser):
			respondError(c, http.StatusForbidden, "account_inactive", "Akun tidak aktif")
		default:
			respondError(c, http.StatusUnauthorized, "invalid_token", "Refresh token tidak valid atau kedaluwarsa")
		}
		return
	}
	respondData(c, http.StatusOK, pair)
}

func (h *Handler) logout(c *gin.Context) {
	userID, _, _ := currentUser(c)
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.RefreshToken != "" {
		if err := h.auth.Logout(c.Request.Context(), userID, req.RefreshToken); err != nil {
			h.log.Warn("logout failed", "error", err)
		}
	}
	h.audit(c, &userID, "auth.logout", "user", userID.String(), nil)
	c.Status(http.StatusNoContent)
}

func (h *Handler) me(c *gin.Context) {
	userID, _, _ := currentUser(c)
	user, err := h.repo.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			respondError(c, http.StatusNotFound, "not_found", "User tidak ditemukan")
			return
		}
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondData(c, http.StatusOK, user)
}

func (h *Handler) listUsers(c *gin.Context) {
	page, perPage, limit, offset := pageParams(c)
	users, total, err := h.auth.ListUsers(c.Request.Context(), limit, offset)
	if err != nil {
		h.log.Error("list users failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondList(c, users, page, perPage, total)
}

func (h *Handler) createUser(c *gin.Context) {
	userID, _, _ := currentUser(c)
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
		Name     string `json:"name"`
		Role     string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_request", "Email, password, dan role wajib valid")
		return
	}
	role := domain.UserRole(strings.ToLower(strings.TrimSpace(req.Role)))
	if role == "" {
		role = domain.RoleViewer
	}
	user, err := h.auth.CreateUser(c.Request.Context(), service.CreateUserInput{
		Email:    req.Email,
		Password: req.Password,
		Name:     req.Name,
		Role:     role,
	}, &userID, c.ClientIP())
	if err != nil {
		if errors.Is(err, service.ErrValidation) {
			respondError(c, http.StatusUnprocessableEntity, "validation_error", err.Error())
			return
		}
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			respondError(c, http.StatusConflict, "duplicate_email", "Email sudah terdaftar")
			return
		}
		h.log.Error("create user failed", "error", err)
		respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		return
	}
	respondData(c, http.StatusCreated, user)
}

func (h *Handler) updateUser(c *gin.Context) {
	actorID, _, _ := currentUser(c)
	id, ok := parseUUIDParam(c, "userID")
	if !ok {
		return
	}
	var req struct {
		Name     *string `json:"name"`
		Role     *string `json:"role"`
		IsActive *bool   `json:"is_active"`
		Password *string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_request", "Payload tidak valid")
		return
	}
	in := service.UpdateUserInput{Name: req.Name, IsActive: req.IsActive, Password: req.Password}
	if req.Role != nil {
		role := domain.UserRole(strings.ToLower(strings.TrimSpace(*req.Role)))
		in.Role = &role
	}
	user, err := h.auth.UpdateUser(c.Request.Context(), id, in, &actorID, c.ClientIP())
	if err != nil {
		switch {
		case errors.Is(err, service.ErrValidation):
			respondError(c, http.StatusUnprocessableEntity, "validation_error", err.Error())
		case errors.Is(err, repository.ErrNotFound):
			respondError(c, http.StatusNotFound, "not_found", "User tidak ditemukan")
		default:
			h.log.Error("update user failed", "error", err)
			respondError(c, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan internal")
		}
		return
	}
	respondData(c, http.StatusOK, user)
}
