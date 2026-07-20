package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"platform.local/capability-platform/backend/internal/testsupport/g2a06acceptance"
)

const manifestVersion = "g2a06.acceptance.v1"
const reservationRecord = `{"version":"g2a06.acceptance.reservation.v1","stage":"reserved"}`
const reservationPreparingRecord = `{"version":"g2a06.acceptance.reservation.v1","stage":"preparing"}`

type acceptanceManifest struct {
	Version                   string `json:"version"`
	AuthInteractionID         string `json:"auth_interaction_id"`
	NegativeAuthInteractionID string `json:"negative_auth_interaction_id"`
	AccountInteractionID      string `json:"account_interaction_id"`
	Nonce                     string `json:"nonce"`
	Ciphertext                string `json:"ciphertext"`
}

type controlledRaceHooks struct {
	afterValidateBeforeCreate func(string)
	afterSecureBeforeWrite    func(string)
	afterCloseBeforeMove      func(string)
	afterMoveBeforePostcheck  func(string)
	afterVerifyBeforeRead     func(string)
	afterReadBeforePostcheck  func(string)
	afterReservationParse     func(string)
	afterVerifyBeforeRemove   func(string)
	afterReservationWrite     func(string)
	reservationSecure         func(string, bool) error
	reservationWrite          func(*os.File, string) error
	reservationSync           func(*os.File) error
	replacementPostcheck      func() error
	cleanupReplacementError   bool
	afterPreparingValidated   func(string)
}

type manifestPayload struct {
	ClientSessionID, ClientToken, CodeVerifier, NegativeCodeVerifier string
	AccountClientSessionID, AccountClientToken                       string
	AuthState, NegativeAuthState, AccountState                       string
	ProductID, ApplicationID, TenantID, UserID, UserSessionID        string
	AccountApplicationID, AccountUserSessionID                       string
	Password                                                         string
	Stage, PositiveCode, PositiveState                               string
	AccountCode, AccountCompletionState                              string
}

const (
	stagePrepared              = "prepared"
	stageWrongVerified         = "wrong_verified"
	stageReady                 = "ready"
	stageExchanged             = "exchanged"
	stageReplayVerified        = "replay_verified"
	stageAccountReady          = "account_ready"
	stageAccountExchanged      = "account_exchanged"
	stageAccountReplayVerified = "account_replay_verified"
	stageCompensated           = "compensated"
)

func manifestFile(root string) string {
	return filepath.Join(root, ".runtime", "G2A-06", "acceptance-manifest.json")
}

func reservationFile(root string) string {
	return filepath.Join(root, ".runtime", "G2A-06", "acceptance-reservation.json")
}

func reserveAcceptance(root string) error {
	return reserveAcceptanceWithHooks(root, nil)
}

func reserveAcceptanceWithHooks(root string, hooks *controlledRaceHooks) error {
	directory := filepath.Dir(manifestFile(root))
	if err := ensureControlledDirectoryWithHooks(root, directory, hooks); err != nil {
		return err
	}
	guard, err := lockRuntimeDirectory(directory)
	if err != nil {
		return err
	}
	defer guard.Close()
	for _, path := range []string{manifestFile(root), reservationFile(root)} {
		if _, err := os.Lstat(path); err == nil {
			return errors.New("G2A-06 acceptance state already exists")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	reservationMatches, err := filepath.Glob(reservationFile(root) + "*")
	if err != nil || len(reservationMatches) != 0 {
		return errors.New("G2A-06 acceptance reservation prefix state exists")
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".acceptance-*.tmp-*"))
	if err != nil || len(matches) != 0 {
		return errors.New("G2A-06 acceptance temporary state exists")
	}
	if hooks != nil && hooks.afterValidateBeforeCreate != nil {
		hooks.afterValidateBeforeCreate(reservationFile(root))
	}
	if err = guard.Verify(); err != nil {
		return err
	}
	file, err := openNewRuntimeFile(reservationFile(root))
	if err != nil {
		return errors.New("reserve G2A-06 acceptance state")
	}
	identity, identityErr := openRuntimeFileIdentity(file)
	if identityErr != nil {
		_ = file.Close()
		return errors.New("validate G2A-06 acceptance reservation")
	}
	secure := secureRuntimePath
	if hooks != nil && hooks.reservationSecure != nil {
		secure = hooks.reservationSecure
	}
	if err = secure(reservationFile(root), false); err == nil {
		if hooks != nil && hooks.afterSecureBeforeWrite != nil {
			hooks.afterSecureBeforeWrite(reservationFile(root))
		}
		if err = validateOpenRuntimeFile(file); err != nil {
			return cleanupFailedReservation(file, reservationFile(root), identity, err)
		}
		if hooks != nil && hooks.reservationWrite != nil {
			err = hooks.reservationWrite(file, reservationRecord)
		} else {
			_, err = io.WriteString(file, reservationRecord)
		}
	}
	if err != nil {
		return cleanupFailedReservation(file, reservationFile(root), identity, err)
	}
	if hooks != nil && hooks.reservationSync != nil {
		err = hooks.reservationSync(file)
	} else {
		err = file.Sync()
	}
	if err != nil {
		return cleanupFailedReservation(file, reservationFile(root), identity, err)
	}
	if hooks != nil && hooks.afterReservationWrite != nil {
		hooks.afterReservationWrite(reservationFile(root))
	}
	if current, currentErr := openRuntimeFileIdentity(file); currentErr != nil || !sameRuntimeFileIdentity(current, identity) || validateRuntimeFileIdentity(reservationFile(root), identity) != nil {
		if currentErr != nil {
			err = currentErr
		} else {
			err = errors.New("controlled reservation identity changed")
		}
		return cleanupFailedReservation(file, reservationFile(root), identity, err)
	}
	if err = guard.Verify(); err != nil {
		return cleanupFailedReservation(file, reservationFile(root), identity, err)
	}
	if err = validateReservationNamespace(root, true); err != nil {
		return cleanupFailedReservation(file, reservationFile(root), identity, err)
	}
	if closeErr := file.Close(); closeErr != nil {
		return errors.New("close recoverable G2A-06 acceptance reservation")
	}
	if err = syncDirectory(directory); err != nil {
		return errors.New("sync recoverable G2A-06 acceptance reservation")
	}
	return nil
}

func validateReservationNamespace(root string, requireExact bool) error {
	prefixMatches, err := filepath.Glob(reservationFile(root) + "*")
	if err != nil {
		return errors.New("inspect G2A-06 acceptance reservation namespace")
	}
	if requireExact {
		if len(prefixMatches) != 1 || !strings.EqualFold(filepath.Clean(prefixMatches[0]), filepath.Clean(reservationFile(root))) {
			return errors.New("G2A-06 acceptance reservation namespace is ambiguous")
		}
	} else if len(prefixMatches) != 0 {
		return errors.New("G2A-06 acceptance reservation namespace is occupied")
	}
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(reservationFile(root)), ".acceptance-*.tmp-*"))
	if err != nil || len(temporary) != 0 {
		return errors.New("G2A-06 acceptance temporary namespace is occupied")
	}
	return nil
}

func cleanupFailedReservation(file *os.File, path string, identity runtimeFileIdentity, cause error) error {
	cleanupErr := removeOpenRuntimeFile(file, path, identity)
	closeErr := file.Close()
	if cleanupErr != nil || closeErr != nil {
		return fmt.Errorf("persist G2A-06 acceptance reservation failed safely; reservation cleanup failed: %w", cause)
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		return fmt.Errorf("persist G2A-06 acceptance reservation failed safely; original reservation entry remains: %w", cause)
	}
	if syncErr := syncDirectory(filepath.Dir(path)); syncErr != nil {
		return fmt.Errorf("persist G2A-06 acceptance reservation failed after identity-safe cleanup: %w", cause)
	}
	return fmt.Errorf("persist G2A-06 acceptance reservation: %w", cause)
}

// recoverReservationOnly removes only the exact pre-fixture reservation record.
// It never scans for database objects and refuses ambiguous manifest/temp state.
func markAcceptancePreparing(root string) error {
	return markAcceptancePreparingWithHooks(root, nil)
}

func markAcceptancePreparingWithHooks(root string, hooks *controlledRaceHooks) error {
	directory := filepath.Dir(reservationFile(root))
	path, err := controlledFile(root, directory, reservationFile(root))
	if err != nil {
		return errors.New("controlled reservation state is unavailable")
	}
	raw, err := readControlledFile(path)
	if err != nil {
		return errors.New("read controlled reservation state")
	}
	if string(raw) != reservationRecord {
		clear(raw)
		return errors.New("acceptance reservation is not ready for prepare")
	}
	clear(raw)
	guard, err := lockRuntimeDirectory(directory)
	if err != nil {
		return err
	}
	defer guard.Close()
	if err = guard.Verify(); err != nil {
		return err
	}
	raw, err = readControlledFile(path)
	if err != nil {
		return errors.New("revalidate controlled reservation state")
	}
	if string(raw) != reservationRecord {
		clear(raw)
		return errors.New("acceptance reservation changed before prepare")
	}
	clear(raw)
	markHooks := &controlledRaceHooks{cleanupReplacementError: true}
	if hooks != nil {
		markHooks.afterSecureBeforeWrite = hooks.afterSecureBeforeWrite
		markHooks.afterCloseBeforeMove = hooks.afterCloseBeforeMove
		markHooks.afterMoveBeforePostcheck = hooks.afterMoveBeforePostcheck
		markHooks.replacementPostcheck = hooks.replacementPostcheck
	}
	if err = atomicWriteControlledGuard(root, path, []byte(reservationPreparingRecord), guard, markHooks); err != nil {
		return fmt.Errorf("persist preparing reservation state: %w", err)
	}
	return nil
}

func recoverReservationOnly(root string) (bool, error) {
	return recoverReservationOnlyWithHooks(root, nil)
}

func validatePreparingReservation(root string) error {
	return validatePreparingReservationWithHooks(root, nil)
}

func validatePreparingReservationWithHooks(root string, hooks *controlledRaceHooks) error {
	directory := filepath.Dir(reservationFile(root))
	guard, err := lockRuntimeDirectory(directory)
	if err != nil {
		return err
	}
	defer guard.Close()
	if err = guard.Verify(); err != nil {
		return err
	}
	if _, err = os.Lstat(manifestFile(root)); err == nil {
		return errors.New("acceptance manifest exists before database prepare")
	} else if !os.IsNotExist(err) {
		return errors.New("inspect acceptance manifest before database prepare")
	}
	path, err := controlledFile(root, directory, reservationFile(root))
	if err != nil {
		return errors.New("controlled preparing reservation is unavailable")
	}
	file, err := openRecoveryRuntimeFile(path)
	if err != nil {
		return errors.New("open controlled preparing reservation")
	}
	identity, err := openRuntimeFileIdentity(file)
	if err != nil {
		_ = file.Close()
		return errors.New("validate controlled preparing reservation handle")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || len(raw) > 64*1024 || string(raw) != reservationPreparingRecord {
		clear(raw)
		_ = file.Close()
		return errors.New("controlled preparing reservation stage is invalid")
	}
	clear(raw)
	if hooks != nil && hooks.afterPreparingValidated != nil {
		hooks.afterPreparingValidated(path)
	}
	current, currentErr := openRuntimeFileIdentity(file)
	if currentErr != nil || !sameRuntimeFileIdentity(current, identity) || validateOpenRuntimeFilePath(file, path, identity) != nil {
		_ = file.Close()
		return errors.New("controlled preparing reservation identity changed")
	}
	if err = guard.Verify(); err != nil {
		_ = file.Close()
		return err
	}
	if err = validateReservationNamespace(root, true); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return errors.New("close controlled preparing reservation handle")
	}
	return nil
}

func recoverReservationOnlyWithHooks(root string, hooks *controlledRaceHooks) (bool, error) {
	directory := filepath.Dir(reservationFile(root))
	manifestExists := false
	if _, err := os.Lstat(manifestFile(root)); err == nil {
		manifestExists = true
	} else if !os.IsNotExist(err) {
		return false, errors.New("inspect acceptance manifest before reservation recovery")
	}
	if _, err := os.Lstat(reservationFile(root)); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, errors.New("inspect reservation-only state")
	}
	guard, err := lockRuntimeDirectory(directory)
	if err != nil {
		return false, err
	}
	defer guard.Close()
	if err = guard.Verify(); err != nil {
		return false, err
	}
	if _, err = os.Lstat(manifestFile(root)); err == nil {
		manifestExists = true
	} else if !os.IsNotExist(err) {
		return false, errors.New("reinspect acceptance manifest before reservation recovery")
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".acceptance-*.tmp-*"))
	if err != nil || len(matches) != 0 {
		return false, errors.New("ambiguous acceptance temporary state")
	}
	path, err := controlledFile(root, directory, reservationFile(root))
	if err != nil {
		return false, errors.New("controlled reservation-only state is unavailable")
	}
	file, err := openRecoveryRuntimeFile(path)
	if err != nil {
		return false, errors.New("open controlled reservation-only state")
	}
	identity, err := openRuntimeFileIdentity(file)
	if err != nil {
		_ = file.Close()
		return false, errors.New("validate controlled reservation-only handle")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || len(raw) > 64*1024 {
		_ = file.Close()
		clear(raw)
		return false, errors.New("read controlled reservation-only state")
	}
	defer clear(raw)
	if hooks != nil && hooks.afterReservationParse != nil {
		hooks.afterReservationParse(path)
	}
	current, currentErr := openRuntimeFileIdentity(file)
	if currentErr != nil || !sameRuntimeFileIdentity(current, identity) || validateOpenRuntimeFilePath(file, path, identity) != nil {
		_ = file.Close()
		return false, errors.New("controlled reservation-only state changed during inspection")
	}
	switch string(raw) {
	case reservationPreparingRecord:
		if closeErr := file.Close(); closeErr != nil {
			return false, errors.New("close preparing reservation inspection handle")
		}
		return false, errors.New("acceptance reservation is preparing; manual audit required")
	case reservationRecord:
		if manifestExists {
			if closeErr := file.Close(); closeErr != nil {
				return false, errors.New("close ambiguous reservation inspection handle")
			}
			return false, errors.New("reserved marker conflicts with acceptance manifest; manual audit required")
		}
	default:
		isEncrypted := looksLikeEncryptedManifestEnvelope(raw)
		if closeErr := file.Close(); closeErr != nil {
			return false, errors.New("close reservation inspection handle")
		}
		if isEncrypted {
			return false, nil
		}
		return false, errors.New("unrecognized reservation-only state; manual audit required")
	}
	if err = removeOpenRuntimeFile(file, path, identity); err != nil {
		_ = file.Close()
		return false, errors.New("remove controlled reservation-only state")
	}
	if err = file.Close(); err != nil {
		return false, errors.New("close removed reservation-only state")
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		return false, errors.New("controlled reservation-only state remains")
	}
	if err = syncDirectory(directory); err != nil {
		return false, errors.New("sync reservation-only recovery")
	}
	return true, nil
}

func looksLikeEncryptedManifestEnvelope(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var m acceptanceManifest
	if decoder.Decode(&m) != nil || decoder.Decode(&struct{}{}) != io.EOF || m.Version != manifestVersion || m.AuthInteractionID == "" || m.NegativeAuthInteractionID == "" || m.AccountInteractionID == "" {
		return false
	}
	nonce, nonceErr := base64.RawURLEncoding.DecodeString(m.Nonce)
	ciphertext, ciphertextErr := base64.RawURLEncoding.DecodeString(m.Ciphertext)
	return nonceErr == nil && len(nonce) > 0 && ciphertextErr == nil && len(ciphertext) > 0
}

func writeManifest(root, pepper string, prepared g2a06acceptance.Result, password []byte) error {
	payload := manifestPayload{ClientSessionID: prepared.ClientSessionID, ClientToken: prepared.ClientToken, AccountClientSessionID: prepared.AccountClientSessionID, AccountClientToken: prepared.AccountClientToken, CodeVerifier: prepared.CodeVerifier, NegativeCodeVerifier: prepared.NegativeCodeVerifier, AuthState: prepared.AuthState, NegativeAuthState: prepared.NegativeAuthState, AccountState: prepared.AccountState, ProductID: prepared.ProductID, ApplicationID: prepared.ApplicationID, AccountApplicationID: prepared.AccountApplicationID, TenantID: prepared.TenantID, UserID: prepared.UserID, UserSessionID: prepared.UserSessionID, AccountUserSessionID: prepared.AccountUserSessionID, Password: string(password), Stage: stagePrepared}
	m := acceptanceManifest{Version: manifestVersion, AuthInteractionID: prepared.AuthInteractionID, NegativeAuthInteractionID: prepared.NegativeAuthInteractionID, AccountInteractionID: prepared.AccountInteractionID}
	return persistManifest(root, pepper, m, payload)
}

func persistManifest(root, pepper string, m acceptanceManifest, payload manifestPayload) error {
	return persistManifestWithHooks(root, pepper, m, payload, nil)
}

func persistManifestWithHooks(root, pepper string, m acceptanceManifest, payload manifestPayload, hooks *controlledRaceHooks) error {
	plain, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	defer clear(plain)
	aead, err := manifestAEAD(pepper)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	m.Version = manifestVersion
	m.Nonce = base64.RawURLEncoding.EncodeToString(nonce)
	aad := manifestAAD(m)
	m.Ciphertext = base64.RawURLEncoding.EncodeToString(aead.Seal(nil, nonce, plain, aad))
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	directory := filepath.Dir(manifestFile(root))
	if err = ensureControlledDirectoryWithHooks(root, directory, hooks); err != nil {
		return err
	}
	guard, err := lockRuntimeDirectory(directory)
	if err != nil {
		return err
	}
	defer guard.Close()
	// The recovery record is authoritative and is replaced before the public
	// manifest so an interrupted state transition never moves backwards.
	if err = atomicWriteControlledGuard(root, reservationFile(root), raw, guard, hooks); err != nil {
		return fmt.Errorf("persist acceptance recovery record: %w", err)
	}
	if err = atomicWriteControlledGuard(root, manifestFile(root), raw, guard, hooks); err != nil {
		return fmt.Errorf("persist acceptance manifest: %w", err)
	}
	return nil
}

func readManifest(root, pepper string) (acceptanceManifest, manifestPayload, error) {
	manifest, payload, manifestErr := readManifestFile(root, pepper, manifestFile(root))
	recovery, recoveryPayload, recoveryErr := readManifestFile(root, pepper, reservationFile(root))
	if recoveryErr == nil && (manifestErr != nil || stageRank(recoveryPayload.Stage) > stageRank(payload.Stage)) {
		return recovery, recoveryPayload, nil
	}
	return manifest, payload, manifestErr
}

func readManifestFile(root, pepper, rawPath string) (acceptanceManifest, manifestPayload, error) {
	var m acceptanceManifest
	var payload manifestPayload
	path, err := controlledFile(root, filepath.Join(root, ".runtime", "G2A-06"), rawPath)
	if err != nil {
		return m, payload, err
	}
	raw, err := readControlledFileWithHooks(path, nil)
	if err != nil {
		return m, payload, err
	}
	defer clear(raw)
	if json.Unmarshal(raw, &m) != nil || m.Version != manifestVersion || m.AuthInteractionID == "" || m.NegativeAuthInteractionID == "" || m.AccountInteractionID == "" {
		return m, payload, errors.New("invalid acceptance manifest")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(m.Nonce)
	if err != nil {
		return m, payload, errors.New("invalid acceptance manifest")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(m.Ciphertext)
	if err != nil {
		return m, payload, errors.New("invalid acceptance manifest")
	}
	defer clear(ciphertext)
	aead, err := manifestAEAD(pepper)
	if err != nil {
		return m, payload, err
	}
	plain, err := aead.Open(nil, nonce, ciphertext, manifestAAD(m))
	if err != nil {
		return m, payload, errors.New("acceptance manifest authentication failed")
	}
	defer clear(plain)
	if json.Unmarshal(plain, &payload) != nil {
		return m, payload, errors.New("invalid acceptance manifest payload")
	}
	return m, payload, nil
}

func stageRank(stage string) int {
	for index, value := range []string{stagePrepared, stageWrongVerified, stageReady, stageExchanged, stageReplayVerified, stageAccountReady, stageAccountExchanged, stageAccountReplayVerified, stageCompensated} {
		if stage == value {
			return index + 1
		}
	}
	return 0
}

func atomicWriteControlled(root, path string, raw []byte) error {
	directory := filepath.Dir(path)
	if err := ensureControlledDirectory(root, directory); err != nil {
		return err
	}
	guard, err := lockRuntimeDirectory(directory)
	if err != nil {
		return err
	}
	defer guard.Close()
	return atomicWriteControlledGuard(root, path, raw, guard, nil)
}

func atomicWriteControlledGuard(_ string, path string, raw []byte, guard *runtimeDirectoryGuard, hooks *controlledRaceHooks) error {
	if err := guard.Verify(); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	tmp, file, err := createControlledTemp(directory)
	if err != nil {
		return fmt.Errorf("create controlled temporary file: %w", err)
	}
	defer removeRuntimeFile(tmp)
	if err = secureRuntimePath(tmp, false); err == nil {
		if hooks != nil && hooks.afterSecureBeforeWrite != nil {
			hooks.afterSecureBeforeWrite(tmp)
		}
		if err = validateOpenRuntimeFile(file); err != nil {
			_ = file.Close()
			return err
		}
		_, err = file.Write(raw)
	}
	if syncErr := file.Sync(); err == nil {
		err = syncErr
	}
	identity, identityErr := openRuntimeFileIdentity(file)
	if err == nil {
		err = identityErr
	}
	if err != nil {
		_ = file.Close()
		return err
	}
	if hooks != nil && hooks.afterCloseBeforeMove != nil {
		hooks.afterCloseBeforeMove(tmp)
	}
	if err = guard.Verify(); err != nil || validateOpenRuntimeFile(file) != nil || validateRuntimeFileIdentity(tmp, identity) != nil {
		_ = file.Close()
		return errors.New("controlled temporary file changed before replacement")
	}
	var moveHook func(string)
	var replacementPostcheck func() error
	cleanupReplacementError := false
	if hooks != nil {
		moveHook = hooks.afterMoveBeforePostcheck
		replacementPostcheck = hooks.replacementPostcheck
		cleanupReplacementError = hooks.cleanupReplacementError
	}
	if err = replaceOpenRuntimeFile(file, tmp, path, identity, moveHook, replacementPostcheck, cleanupReplacementError); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return errors.Join(fmt.Errorf("replace controlled file: %w", err), errors.New("close failed controlled replacement"))
		}
		if cleanupReplacementError {
			if syncErr := syncDirectory(directory); syncErr != nil {
				return errors.Join(fmt.Errorf("replace controlled file: %w", err), errors.New("sync failed controlled replacement cleanup"))
			}
			prefixMatches, prefixErr := filepath.Glob(path + "*")
			if prefixErr != nil {
				return errors.Join(fmt.Errorf("replace controlled file: %w", err), errors.New("inspect controlled replacement cleanup"))
			}
			if len(prefixMatches) != 0 && !(len(prefixMatches) == 1 && strings.EqualFold(filepath.Clean(prefixMatches[0]), filepath.Clean(path))) {
				return errors.Join(fmt.Errorf("replace controlled file: %w", err), errors.New("controlled replacement prefix state remains"))
			}
		}
		return fmt.Errorf("replace controlled file: %w", err)
	}
	if err = guard.Verify(); err != nil || validateRuntimeFileIdentity(path, identity) != nil || verifyRuntimeFileSecurity(path) != nil {
		_ = file.Close()
		return errors.New("controlled manifest changed after replacement")
	}
	if closeErr := file.Close(); closeErr != nil {
		return closeErr
	}
	if err = syncDirectory(directory); err != nil {
		return fmt.Errorf("sync controlled directory: %w", err)
	}
	return nil
}

func removeAcceptanceState(root string) error {
	return removeAcceptanceStateWithHooks(root, nil)
}

func removeAcceptanceStateWithHooks(root string, hooks *controlledRaceHooks) error {
	directory := filepath.Dir(manifestFile(root))
	guard, err := lockRuntimeDirectory(directory)
	if err != nil {
		return err
	}
	defer guard.Close()
	for _, path := range []string{manifestFile(root), reservationFile(root)} {
		if validateErr := validateRuntimeFile(path); validateErr != nil {
			if errors.Is(validateErr, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("verify controlled acceptance state")
		}
		if err := guard.Verify(); err != nil {
			return err
		}
		var removeHook func(string)
		if hooks != nil {
			removeHook = hooks.afterVerifyBeforeRemove
		}
		if err := removeRuntimeFileWithHook(path, removeHook); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove controlled acceptance state")
		}
	}
	return syncDirectory(directory)
}

func createControlledTemp(directory string) (string, *os.File, error) {
	for attempt := 0; attempt < 16; attempt++ {
		value := make([]byte, 12)
		if _, err := rand.Read(value); err != nil {
			return "", nil, err
		}
		path := filepath.Join(directory, ".acceptance-tmp-"+base64.RawURLEncoding.EncodeToString(value))
		file, err := openNewRuntimeFile(path)
		if err == nil {
			return path, file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, err
		}
	}
	return "", nil, errors.New("create controlled temporary file")
}

func readControlledFileWithHooks(path string, hooks *controlledRaceHooks) ([]byte, error) {
	file, err := openReadRuntimeFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	identity, err := openRuntimeFileIdentity(file)
	if err != nil {
		return nil, err
	}
	if hooks != nil && hooks.afterVerifyBeforeRead != nil {
		hooks.afterVerifyBeforeRead(path)
	}
	if current, currentErr := openRuntimeFileIdentity(file); currentErr != nil || !sameRuntimeFileIdentity(current, identity) {
		if currentErr != nil {
			return nil, currentErr
		}
		return nil, errors.New("controlled file identity changed before read")
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	if hooks != nil && hooks.afterReadBeforePostcheck != nil {
		hooks.afterReadBeforePostcheck(path)
	}
	current, currentErr := openRuntimeFileIdentity(file)
	if currentErr != nil || !sameRuntimeFileIdentity(current, identity) || validateRuntimeFileIdentity(path, identity) != nil {
		clear(raw)
		return nil, errors.New("controlled file identity changed during read")
	}
	return raw, nil
}

func readControlledFile(path string) ([]byte, error) { return readControlledFileWithHooks(path, nil) }

func manifestAEAD(pepper string) (cipher.AEAD, error) {
	key := sha256.Sum256([]byte("g2a06.acceptance-manifest-key.v1\x00" + pepper))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func manifestAAD(m acceptanceManifest) []byte {
	return []byte(strings.Join([]string{m.Version, m.AuthInteractionID, m.NegativeAuthInteractionID, m.AccountInteractionID}, "\x00"))
}

func ensureControlledDirectory(root, target string) error {
	return ensureControlledDirectoryWithHooks(root, target, nil)
}

func ensureControlledDirectoryWithHook(root, target string, stepHook func(string)) error {
	return ensureControlledDirectoryWithHooks(root, target, &controlledRaceHooks{afterMoveBeforePostcheck: stepHook})
}

func ensureControlledDirectoryWithHooks(root, target string, hooks *controlledRaceHooks) error {
	runtimeRoot, err := filepath.Abs(filepath.Join(root, ".runtime"))
	if err != nil || validateControlledDirectory(runtimeRoot, runtimeRoot) != nil {
		return errors.New("runtime root is unavailable or linked")
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return errors.New("resolve controlled runtime path")
	}
	rel, err := filepath.Rel(runtimeRoot, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("runtime path escapes repository")
	}
	current := runtimeRoot
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." || validateControlledDirectory(runtimeRoot, current) != nil {
			return errors.New("runtime path contains link")
		}
		next := filepath.Join(current, part)
		info, statErr := os.Lstat(next)
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if statErr == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return errors.New("runtime path contains link or non-directory")
		}
		// Revalidate the parent immediately before mutation so a replacement
		// observed between path steps fails closed.
		if validateControlledDirectory(runtimeRoot, current) != nil {
			return errors.New("runtime parent changed during validation")
		}
		if hooks != nil && hooks.afterValidateBeforeCreate != nil && os.IsNotExist(statErr) {
			hooks.afterValidateBeforeCreate(next)
		}
		if validateControlledDirectory(runtimeRoot, current) != nil {
			return errors.New("runtime parent changed before creation")
		}
		if os.IsNotExist(statErr) && os.Mkdir(next, 0700) != nil {
			return errors.New("create controlled runtime directory")
		}
		if validateControlledDirectory(runtimeRoot, next) != nil {
			return errors.New("runtime path contains reparse escape")
		}
		if err := secureRuntimePath(next, true); err != nil {
			return errors.New("protect controlled runtime directory")
		}
		if hooks != nil && hooks.afterMoveBeforePostcheck != nil {
			hooks.afterMoveBeforePostcheck(next)
		}
		if validateControlledDirectory(runtimeRoot, runtimeRoot) != nil || validateControlledDirectory(runtimeRoot, next) != nil {
			return errors.New("runtime path changed during protection")
		}
		current = next
	}
	return nil
}

func validateControlledDirectory(runtimeRoot, candidate string) error {
	root, err := filepath.Abs(runtimeRoot)
	if err != nil {
		return err
	}
	path, err := filepath.Abs(candidate)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("controlled directory escapes runtime root")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("controlled directory is unavailable or linked")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !strings.EqualFold(filepath.Clean(path), filepath.Clean(resolved)) {
		return errors.New("controlled directory contains reparse escape")
	}
	return nil
}
