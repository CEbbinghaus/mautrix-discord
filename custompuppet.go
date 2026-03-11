package main

import (
	"errors"

	"maunium.net/go/mautrix/bridge"
	"maunium.net/go/mautrix/id"
)

func (puppet *Puppet) SwitchCustomMXID(accessToken string, mxid id.UserID) error {
	// If the MXID is changing, the stored token belongs to the old identity
	// and must be invalidated. Only preserve it when re-linking the same MXID
	// without supplying a new token (e.g. an admin correcting other fields
	// while leaving an already-working double-puppet setup intact).
	if accessToken != "" {
		puppet.AccessToken = accessToken
	} else if puppet.CustomMXID != mxid {
		puppet.AccessToken = ""
	}
	puppet.CustomMXID = mxid
	puppet.Update()
	err := puppet.StartCustomMXID(false)
	if err != nil {
		return err
	}
	// TODO leave rooms with default puppet
	return nil
}

func (puppet *Puppet) ClearCustomMXID() {
	save := puppet.CustomMXID != "" || puppet.AccessToken != ""
	puppet.bridge.puppetsLock.Lock()
	if puppet.CustomMXID != "" && puppet.bridge.puppetsByCustomMXID[puppet.CustomMXID] == puppet {
		delete(puppet.bridge.puppetsByCustomMXID, puppet.CustomMXID)
	}
	puppet.bridge.puppetsLock.Unlock()
	puppet.CustomMXID = ""
	puppet.AccessToken = ""
	puppet.customIntent = nil
	puppet.customUser = nil
	if save {
		puppet.Update()
	}
}

func (puppet *Puppet) StartCustomMXID(reloginOnFail bool) error {
	newIntent, newAccessToken, err := puppet.bridge.DoublePuppet.Setup(puppet.CustomMXID, puppet.AccessToken, reloginOnFail)
	if err != nil {
		if errors.Is(err, bridge.ErrNoAccessToken) {
			// Preserve the link in the database — no token or shared secret
			// available yet. The intent will be activated on the next bridge
			// start or when the user logs in. However, clear any stale runtime
			// state so the bridge does not keep using a previously-active
			// (and now outdated) custom identity.
			puppet.bridge.puppetsLock.Lock()
			if puppet.CustomMXID != "" && puppet.bridge.puppetsByCustomMXID[puppet.CustomMXID] == puppet {
				delete(puppet.bridge.puppetsByCustomMXID, puppet.CustomMXID)
			}
			puppet.bridge.puppetsLock.Unlock()
			puppet.customIntent = nil
			puppet.customUser = nil
		} else {
			puppet.ClearCustomMXID()
		}
		return err
	}
	puppet.bridge.puppetsLock.Lock()
	puppet.bridge.puppetsByCustomMXID[puppet.CustomMXID] = puppet
	puppet.bridge.puppetsLock.Unlock()
	if puppet.AccessToken != newAccessToken {
		puppet.AccessToken = newAccessToken
		puppet.Update()
	}
	puppet.customIntent = newIntent
	puppet.customUser = puppet.bridge.GetUserByMXID(puppet.CustomMXID)
	return nil
}

func (user *User) tryAutomaticDoublePuppeting() {
	if !user.bridge.Config.CanAutoDoublePuppet(user.MXID) {
		return
	}
	user.log.Debug().Msg("Checking if double puppeting needs to be enabled")
	puppet := user.bridge.GetPuppetByID(user.DiscordID)
	if len(puppet.CustomMXID) > 0 {
		if puppet.customIntent == nil {
			// CustomMXID was set (e.g. via link command) but the intent was
			// not yet activated. Try to activate it now that a user is online.
			user.log.Debug().Msg("CustomMXID set but intent not active, attempting to activate")
			if err := puppet.StartCustomMXID(true); err != nil {
				if errors.Is(err, bridge.ErrNoAccessToken) {
					// No token or shared secret available yet; the link is
					// preserved and will be retried on the next login.
					user.log.Debug().Msg("Cannot activate custom puppet intent yet: no access token or shared secret available")
				} else {
					// Other errors (e.g. mismatching MXID, invalid token) indicate
					// a genuinely broken link; StartCustomMXID will have cleared it.
					user.log.Warn().Err(err).Msg("Failed to activate custom puppet intent; link cleared")
				}
			} else {
				user.log.Debug().Msg("Successfully activated custom puppet intent")
			}
		} else {
			user.log.Debug().Msg("User already has double-puppeting enabled")
		}
		return
	}
	puppet.CustomMXID = user.MXID
	err := puppet.StartCustomMXID(true)
	if err != nil {
		user.log.Warn().Err(err).Msg("Failed to login with shared secret")
	} else {
		// TODO leave rooms with default puppet
		user.log.Debug().Msg("Successfully automatically enabled custom puppet")
	}
}
