package sessions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	idgen "github.com/ZekromNguyen/skawld-sdk-go/internal/id"
	"github.com/ZekromNguyen/skawld-sdk-go/storage"
)

const (
	protectedPayloadPrefix = "skawld-protected-v2:"
	protectedMetaKey       = "_skawld_protected_payload_v2"
	protectedRecordMarker  = "_skawld_protected_v2"
)

// ProtectedStore encrypts sensitive SessionStore payloads before delegating
// persistence. Only session ownership, identifiers, timestamps, task status,
// and task dependency edges remain visible for indexing and authorization.
type ProtectedStore struct {
	inner     core.SessionStore
	protector storage.DocumentProtector
}

func NewProtectedStore(
	inner core.SessionStore,
	protector storage.DocumentProtector,
) (*ProtectedStore, error) {
	if inner == nil || protector == nil {
		return nil, core.NewConfigError(
			"protected session store requires storage and a document protector",
		)
	}
	return &ProtectedStore{inner: inner, protector: protector}, nil
}

func (s *ProtectedStore) Durable() bool {
	durable, ok := s.inner.(core.DurableSessionStore)
	return ok && durable.Durable()
}

func (*ProtectedStore) Protected() bool { return true }

func (s *ProtectedStore) Create(
	ctx context.Context,
	id string,
	meta map[string]interface{},
) (core.SessionRecord, error) {
	if id == "" {
		var err error
		id, err = idgen.New()
		if err != nil {
			return core.SessionRecord{}, err
		}
	}
	principal, err := protectedPrincipal(ctx)
	if err != nil {
		return core.SessionRecord{}, err
	}
	bound, ok := core.BindPrincipalToSessionMeta(meta, principal)
	if !ok {
		return core.SessionRecord{}, core.NewPermissionError(
			"session metadata conflicts with authenticated identity",
		)
	}
	protected, err := s.protectMeta(ctx, id, bound)
	if err != nil {
		return core.SessionRecord{}, err
	}
	record, err := s.inner.Create(ctx, id, protected)
	if err != nil {
		return core.SessionRecord{}, err
	}
	return s.decodeRecord(ctx, principal, record)
}

func (s *ProtectedStore) Load(
	ctx context.Context,
	id string,
) (core.SessionRecord, bool, error) {
	principal, err := protectedPrincipal(ctx)
	if err != nil {
		return core.SessionRecord{}, false, err
	}
	record, exists, err := s.inner.Load(ctx, id)
	if err != nil || !exists {
		return core.SessionRecord{}, exists, err
	}
	decoded, err := s.decodeRecord(ctx, principal, record)
	return decoded, err == nil, err
}

func (s *ProtectedStore) LoadMessages(
	ctx context.Context,
	id string,
) ([]core.StoredMessage, error) {
	if _, err := s.authorizeSession(ctx, id); err != nil {
		return nil, err
	}
	stored, err := s.inner.LoadMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	output := make([]core.StoredMessage, len(stored))
	for index, item := range stored {
		message, err := s.decodeMessage(ctx, id, item.Message)
		if err != nil {
			return nil, fmt.Errorf(
				"decode protected session message %d: %w", item.Seq, err,
			)
		}
		item.Message = message
		output[index] = item
	}
	return output, nil
}

func (s *ProtectedStore) AppendMessages(
	ctx context.Context,
	id string,
	messages []core.Message,
) ([]core.StoredMessage, error) {
	if _, err := s.authorizeSession(ctx, id); err != nil {
		return nil, err
	}
	protected := make([]core.Message, len(messages))
	for index, message := range messages {
		encoded, err := s.protectValue(
			ctx, protectedBinding(id, "message"), message,
		)
		if err != nil {
			return nil, err
		}
		protected[index] = protectedMessage(encoded)
	}
	stored, err := s.inner.AppendMessages(ctx, id, protected)
	if err != nil {
		return nil, err
	}
	output := make([]core.StoredMessage, len(stored))
	for index, item := range stored {
		message, err := s.decodeMessage(ctx, id, item.Message)
		if err != nil {
			return nil, err
		}
		item.Message = message
		output[index] = item
	}
	return output, nil
}

func (s *ProtectedStore) UpdateMeta(
	ctx context.Context,
	id string,
	updates map[string]interface{},
) (core.SessionRecord, error) {
	record, err := s.authorizeSession(ctx, id)
	if err != nil {
		return core.SessionRecord{}, err
	}
	for key, value := range updates {
		if key == core.SessionMetaTenantID ||
			key == core.SessionMetaActorID {
			if record.Meta[key] != value {
				return core.SessionRecord{}, core.NewPermissionError(
					"session ownership cannot change",
				)
			}
			continue
		}
		if value == nil {
			delete(record.Meta, key)
		} else {
			record.Meta[key] = value
		}
	}
	protected, err := s.protectMeta(ctx, id, record.Meta)
	if err != nil {
		return core.SessionRecord{}, err
	}
	raw, err := s.inner.UpdateMeta(ctx, id, protected)
	if err != nil {
		return core.SessionRecord{}, err
	}
	principal, _ := core.PrincipalFromContext(ctx)
	return s.decodeRecord(ctx, principal, raw)
}

func (s *ProtectedStore) SetInvokedSkills(
	ctx context.Context,
	id string,
	skills []core.InvokedSkillRecord,
) error {
	if _, err := s.authorizeSession(ctx, id); err != nil {
		return err
	}
	protected := make([]core.InvokedSkillRecord, len(skills))
	for index, skill := range skills {
		encoded, err := s.protectValue(
			ctx, protectedBinding(id, "invoked_skill"), skill,
		)
		if err != nil {
			return err
		}
		protected[index] = core.InvokedSkillRecord{
			Name:            protectedRecordMarker,
			SubstitutedBody: encoded,
			InvokedAt:       skill.InvokedAt,
		}
	}
	return s.inner.SetInvokedSkills(ctx, id, protected)
}

func (s *ProtectedStore) List(
	ctx context.Context,
	limit int,
	offset int,
) ([]core.SessionRecord, error) {
	principal, err := protectedPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if offset < 0 {
		offset = 0
	}
	records, err := s.inner.List(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	output := make([]core.SessionRecord, 0)
	for _, record := range records {
		if !core.CanAccessSessionStrict(principal, record.Meta) {
			continue
		}
		decoded, err := s.decodeRecord(ctx, principal, record)
		if err != nil {
			return nil, err
		}
		output = append(output, decoded)
	}
	if offset >= len(output) {
		return nil, nil
	}
	output = output[offset:]
	if limit > 0 && limit < len(output) {
		output = output[:limit]
	}
	return output, nil
}

func (s *ProtectedStore) Delete(
	ctx context.Context,
	id string,
) error {
	if _, err := s.authorizeSession(ctx, id); err != nil {
		return err
	}
	return s.inner.Delete(ctx, id)
}

func (s *ProtectedStore) CreateTask(
	ctx context.Context,
	sessionID string,
	input core.CreateTaskInput,
) (core.Task, error) {
	if _, err := s.authorizeSession(ctx, sessionID); err != nil {
		return core.Task{}, err
	}
	payload := protectedTaskPayload{
		Subject: input.Subject, Description: input.Description,
		ActiveForm: input.ActiveForm, Metadata: input.Metadata,
	}
	encoded, err := s.protectValue(
		ctx, protectedBinding(sessionID, "task"), payload,
	)
	if err != nil {
		return core.Task{}, err
	}
	task, err := s.inner.CreateTask(ctx, sessionID, core.CreateTaskInput{
		Subject: protectedRecordMarker, Description: encoded,
	})
	if err != nil {
		return core.Task{}, err
	}
	return s.decodeTask(ctx, sessionID, task)
}

func (s *ProtectedStore) GetTask(
	ctx context.Context,
	sessionID string,
	taskID string,
) (core.Task, bool, error) {
	if _, err := s.authorizeSession(ctx, sessionID); err != nil {
		return core.Task{}, false, err
	}
	task, exists, err := s.inner.GetTask(ctx, sessionID, taskID)
	if err != nil || !exists {
		return core.Task{}, exists, err
	}
	decoded, err := s.decodeTask(ctx, sessionID, task)
	return decoded, err == nil, err
}

func (s *ProtectedStore) ListTasks(
	ctx context.Context,
	sessionID string,
) ([]core.Task, error) {
	if _, err := s.authorizeSession(ctx, sessionID); err != nil {
		return nil, err
	}
	tasks, err := s.inner.ListTasks(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	output := make([]core.Task, len(tasks))
	for index, task := range tasks {
		decoded, err := s.decodeTask(ctx, sessionID, task)
		if err != nil {
			return nil, err
		}
		output[index] = decoded
	}
	return output, nil
}

func (s *ProtectedStore) UpdateTask(
	ctx context.Context,
	sessionID string,
	taskID string,
	patch core.TaskPatch,
) (core.Task, bool, error) {
	current, exists, err := s.GetTask(ctx, sessionID, taskID)
	if err != nil || !exists {
		return core.Task{}, exists, err
	}
	if patch.Subject != nil || patch.Description != nil ||
		patch.ActiveForm != nil || patch.Owner != nil ||
		patch.Metadata != nil {
		payload := protectedTaskPayload{
			Subject: current.Subject, Description: current.Description,
			ActiveForm: current.ActiveForm, Owner: current.Owner,
			Metadata: cloneMap(current.Metadata),
		}
		if patch.Subject != nil {
			payload.Subject = *patch.Subject
		}
		if patch.Description != nil {
			payload.Description = *patch.Description
		}
		if patch.ActiveForm != nil {
			payload.ActiveForm = *patch.ActiveForm
		}
		if patch.Owner != nil {
			payload.Owner = *patch.Owner
		}
		for key, value := range patch.Metadata {
			if value == nil {
				delete(payload.Metadata, key)
			} else {
				payload.Metadata[key] = value
			}
		}
		encoded, err := s.protectValue(
			ctx, protectedBinding(sessionID, "task"), payload,
		)
		if err != nil {
			return core.Task{}, true, err
		}
		marker := protectedRecordMarker
		empty := ""
		patch.Subject = &marker
		patch.Description = &encoded
		patch.ActiveForm = &empty
		patch.Owner = &empty
		patch.Metadata = nil
	}
	task, exists, err := s.inner.UpdateTask(
		ctx, sessionID, taskID, patch,
	)
	if err != nil || !exists {
		return core.Task{}, exists, err
	}
	decoded, err := s.decodeTask(ctx, sessionID, task)
	return decoded, err == nil, err
}

func (s *ProtectedStore) DeleteTask(
	ctx context.Context,
	sessionID string,
	taskID string,
) (bool, error) {
	if _, err := s.authorizeSession(ctx, sessionID); err != nil {
		return false, err
	}
	return s.inner.DeleteTask(ctx, sessionID, taskID)
}

func (s *ProtectedStore) Close() error { return s.inner.Close() }

type protectedTaskPayload struct {
	Subject     string                 `json:"subject"`
	Description string                 `json:"description"`
	ActiveForm  string                 `json:"active_form,omitempty"`
	Owner       string                 `json:"owner,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type protectedValueEnvelope struct {
	Version  int             `json:"version"`
	TenantID string          `json:"tenant_id"`
	ActorID  string          `json:"actor_id"`
	Binding  string          `json:"binding"`
	Payload  json.RawMessage `json:"payload"`
}

func (s *ProtectedStore) authorizeSession(
	ctx context.Context,
	id string,
) (core.SessionRecord, error) {
	principal, err := protectedPrincipal(ctx)
	if err != nil {
		return core.SessionRecord{}, err
	}
	raw, exists, err := s.inner.Load(ctx, id)
	if err != nil {
		return core.SessionRecord{}, err
	}
	if !exists {
		return core.SessionRecord{}, &core.SkawldError{
			Kind: core.ErrorNotFound, Message: "session not found",
		}
	}
	return s.decodeRecord(ctx, principal, raw)
}

func (s *ProtectedStore) decodeRecord(
	ctx context.Context,
	principal core.Principal,
	record core.SessionRecord,
) (core.SessionRecord, error) {
	if !core.CanAccessSessionStrict(principal, record.Meta) {
		return core.SessionRecord{}, core.NewPermissionError(
			"session belongs to another actor or tenant",
		)
	}
	meta, err := s.decodeMeta(ctx, record.ID, record.Meta)
	if err != nil {
		return core.SessionRecord{}, err
	}
	record.Meta = meta
	for index, skill := range record.InvokedSkills {
		if skill.Name != protectedRecordMarker {
			return core.SessionRecord{}, core.NewPermissionError(
				"unprotected invoked-skill record is not permitted",
			)
		}
		var decoded core.InvokedSkillRecord
		if err := s.unprotectValue(
			ctx, protectedBinding(record.ID, "invoked_skill"),
			skill.SubstitutedBody, &decoded,
		); err != nil {
			return core.SessionRecord{}, err
		}
		record.InvokedSkills[index] = decoded
	}
	return record, nil
}

func (s *ProtectedStore) protectMeta(
	ctx context.Context,
	sessionID string,
	meta map[string]interface{},
) (map[string]interface{}, error) {
	private := make(map[string]interface{}, len(meta))
	for key, value := range meta {
		private[key] = value
	}
	encoded, err := s.protectValue(
		ctx, protectedBinding(sessionID, "metadata"), private,
	)
	if err != nil {
		return nil, err
	}
	principal, _ := core.PrincipalFromContext(ctx)
	return map[string]interface{}{
		core.SessionMetaTenantID: principal.TenantID,
		core.SessionMetaActorID:  principal.ActorID,
		protectedMetaKey:         encoded,
	}, nil
}

func (s *ProtectedStore) decodeMeta(
	ctx context.Context,
	sessionID string,
	meta map[string]interface{},
) (map[string]interface{}, error) {
	for key := range meta {
		if key != core.SessionMetaTenantID &&
			key != core.SessionMetaActorID &&
			key != protectedMetaKey {
			return nil, core.NewPermissionError(
				"unprotected private session metadata is not permitted",
			)
		}
	}
	encoded, ok := meta[protectedMetaKey].(string)
	if !ok {
		return nil, core.NewPermissionError(
			"protected session metadata is missing",
		)
	}
	private := map[string]interface{}{}
	if err := s.unprotectValue(
		ctx, protectedBinding(sessionID, "metadata"), encoded, &private,
	); err != nil {
		return nil, err
	}
	if private[core.SessionMetaTenantID] != meta[core.SessionMetaTenantID] ||
		private[core.SessionMetaActorID] != meta[core.SessionMetaActorID] {
		return nil, core.NewPermissionError(
			"protected session ownership authentication failed",
		)
	}
	return private, nil
}

func protectedMessage(encoded string) core.Message {
	return core.Message{
		Role: "system",
		Content: []core.ContentBlock{{
			Type: core.BlockText, Text: encoded,
			Trust: core.TrustSystemPolicy,
		}},
	}
}

func (s *ProtectedStore) decodeMessage(
	ctx context.Context,
	sessionID string,
	message core.Message,
) (core.Message, error) {
	if message.Role != "system" || len(message.Content) != 1 ||
		message.Content[0].Type != core.BlockText ||
		!strings.HasPrefix(
			message.Content[0].Text, protectedPayloadPrefix,
		) {
		return core.Message{}, core.NewPermissionError(
			"unprotected session message is not permitted",
		)
	}
	var decoded core.Message
	if err := s.unprotectValue(
		ctx, protectedBinding(sessionID, "message"),
		message.Content[0].Text, &decoded,
	); err != nil {
		return core.Message{}, err
	}
	return decoded, nil
}

func (s *ProtectedStore) decodeTask(
	ctx context.Context,
	sessionID string,
	task core.Task,
) (core.Task, error) {
	if task.Subject != protectedRecordMarker {
		return core.Task{}, core.NewPermissionError(
			"unprotected task payload is not permitted",
		)
	}
	var payload protectedTaskPayload
	if err := s.unprotectValue(
		ctx, protectedBinding(sessionID, "task"),
		task.Description, &payload,
	); err != nil {
		return core.Task{}, err
	}
	task.Subject = payload.Subject
	task.Description = payload.Description
	task.ActiveForm = payload.ActiveForm
	task.Owner = payload.Owner
	task.Metadata = payload.Metadata
	return task, nil
}

func (s *ProtectedStore) protectValue(
	ctx context.Context,
	binding string,
	value interface{},
) (string, error) {
	principal, err := protectedPrincipal(ctx)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", &core.SkawldError{
			Kind:    core.ErrorValidation,
			Message: "session payload is not JSON serializable",
			Cause:   err,
		}
	}
	plaintext, err := json.Marshal(protectedValueEnvelope{
		Version: 2, TenantID: principal.TenantID,
		ActorID: principal.ActorID, Binding: binding,
		Payload: payload,
	})
	if err != nil {
		return "", &core.SkawldError{
			Kind:    core.ErrorValidation,
			Message: "session protection envelope is not JSON serializable",
			Cause:   err,
		}
	}
	protected, err := s.protector.Protect(ctx, plaintext)
	if err != nil {
		return "", err
	}
	return protectedPayloadPrefix +
		base64.RawURLEncoding.EncodeToString(protected), nil
}

func (s *ProtectedStore) unprotectValue(
	ctx context.Context,
	binding string,
	encoded string,
	output interface{},
) error {
	if !strings.HasPrefix(encoded, protectedPayloadPrefix) {
		return core.NewPermissionError(
			"unprotected session payload is not permitted",
		)
	}
	raw, err := base64.RawURLEncoding.DecodeString(
		strings.TrimPrefix(encoded, protectedPayloadPrefix),
	)
	if err != nil {
		return core.NewPermissionError(
			"protected session payload encoding is invalid",
		)
	}
	plaintext, err := s.protector.Unprotect(ctx, raw)
	if err != nil {
		return err
	}
	principal, err := protectedPrincipal(ctx)
	if err != nil {
		return err
	}
	var envelope protectedValueEnvelope
	if err := json.Unmarshal(plaintext, &envelope); err != nil {
		return &core.SkawldError{
			Kind:    core.ErrorValidation,
			Message: "protected session envelope is invalid",
			Cause:   err,
		}
	}
	if envelope.Version != 2 ||
		envelope.TenantID != principal.TenantID ||
		envelope.ActorID != principal.ActorID ||
		envelope.Binding != binding ||
		len(envelope.Payload) == 0 {
		return core.NewPermissionError(
			"protected session payload binding authentication failed",
		)
	}
	if err := json.Unmarshal(envelope.Payload, output); err != nil {
		return &core.SkawldError{
			Kind:    core.ErrorValidation,
			Message: "protected session payload is invalid",
			Cause:   err,
		}
	}
	return nil
}

func protectedBinding(sessionID, kind string) string {
	return "session:" + sessionID + ":" + kind
}

func protectedPrincipal(ctx context.Context) (core.Principal, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || !principal.Authenticated() {
		return core.Principal{}, core.NewPermissionError(
			"protected session storage requires an authenticated identity",
		)
	}
	return principal, nil
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	output := make(map[string]interface{}, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

var _ core.ProtectedSessionStore = (*ProtectedStore)(nil)
