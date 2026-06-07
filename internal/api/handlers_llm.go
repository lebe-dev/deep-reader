package api

import (
	"errors"

	"github.com/gofiber/fiber/v3"

	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// listLLMProviders handles GET /api/llm-providers — the configured LLM
// connection profiles, with secrets masked (LLMProvider.View). The active
// profile supplies the connection for every LLM call.
func (s *Server) listLLMProviders(c fiber.Ctx) error {
	providers, err := s.store.ListLLMProviders(c.Context())
	if err != nil {
		return s.serverError(c, "list llm providers", err)
	}
	views := make([]model.LLMProviderView, len(providers))
	for i := range providers {
		views[i] = providers[i].View()
	}
	return c.JSON(fiber.Map{"providers": views})
}

// createLLMProvider handles POST /api/llm-providers. The first profile created
// becomes active automatically.
func (s *Server) createLLMProvider(c fiber.Ctx) error {
	var in model.LLMProviderInput
	if err := c.Bind().Body(&in); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}
	if err := in.Validate(); err != nil {
		return sendError(c, fiber.StatusBadRequest, err.Error())
	}

	p := model.LLMProvider{Name: in.Name, BaseURL: in.BaseURL, Model: in.Model}
	if in.APIKey != nil {
		p.APIKey = *in.APIKey
	}
	created, err := s.store.CreateLLMProvider(c.Context(), p)
	if err != nil {
		return s.serverError(c, "create llm provider", err)
	}
	return c.Status(fiber.StatusCreated).JSON(created.View())
}

// updateLLMProvider handles PATCH /api/llm-providers/:id. A null/omitted api_key
// leaves the stored secret unchanged (write-only key).
func (s *Server) updateLLMProvider(c fiber.Ctx) error {
	id := c.Params("id")
	var in model.LLMProviderInput
	if err := c.Bind().Body(&in); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}
	if err := in.Validate(); err != nil {
		return sendError(c, fiber.StatusBadRequest, err.Error())
	}

	updated, err := s.store.UpdateLLMProvider(c.Context(), id, in)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "provider not found")
		}
		return s.serverError(c, "update llm provider", err)
	}
	return c.JSON(updated.View())
}

// deleteLLMProvider handles DELETE /api/llm-providers/:id. Deleting the active
// profile promotes the newest remaining profile to active.
func (s *Server) deleteLLMProvider(c fiber.Ctx) error {
	id := c.Params("id")
	if err := s.store.DeleteLLMProvider(c.Context(), id); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "provider not found")
		}
		return s.serverError(c, "delete llm provider", err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// activateLLMProvider handles POST /api/llm-providers/:id/activate, making the
// profile the sole active connection.
func (s *Server) activateLLMProvider(c fiber.Ctx) error {
	id := c.Params("id")
	if err := s.store.SetActiveLLMProvider(c.Context(), id); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "provider not found")
		}
		return s.serverError(c, "activate llm provider", err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
