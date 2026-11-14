// Copyright 2026 ProxyGrid
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package org.outline;

import android.content.Context;
import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.Base64;
import androidx.annotation.Nullable;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.security.KeyStore;
import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

/**
 * Stores small secrets (access keys, the account refresh token) encrypted with an AES-256-GCM key
 * that lives in the Android Keystore and never leaves it. Only the ciphertext is kept in a private
 * SharedPreferences file. Keystore keys are not backed up, so a restored ciphertext from another
 * device cannot be decrypted; such entries are dropped and read as missing.
 */
public final class SecretStore {
  private static final String KEYSTORE = "AndroidKeyStore";
  private static final String KEY_ALIAS = "app_secret_store_v1";
  private static final String PREFS = "secret_store_v1";
  private static final int IV_BYTES = 12;
  private static final int TAG_BITS = 128;

  private final SharedPreferences prefs;

  public SecretStore(Context context) {
    this.prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
  }

  private static SecretKey getOrCreateKey() throws Exception {
    KeyStore ks = KeyStore.getInstance(KEYSTORE);
    ks.load(null);
    KeyStore.Entry entry = ks.getEntry(KEY_ALIAS, null);
    if (entry instanceof KeyStore.SecretKeyEntry) {
      return ((KeyStore.SecretKeyEntry) entry).getSecretKey();
    }
    KeyGenerator gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE);
    gen.init(new KeyGenParameterSpec.Builder(
            KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
        .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
        .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
        .setKeySize(256)
        .build());
    return gen.generateKey();
  }

  public synchronized void set(String name, String value) throws Exception {
    Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
    cipher.init(Cipher.ENCRYPT_MODE, getOrCreateKey());
    // Bind the ciphertext to its name, so entries cannot be swapped.
    cipher.updateAAD(name.getBytes(StandardCharsets.UTF_8));
    byte[] iv = cipher.getIV();
    byte[] ct = cipher.doFinal(value.getBytes(StandardCharsets.UTF_8));
    byte[] blob = ByteBuffer.allocate(iv.length + ct.length).put(iv).put(ct).array();
    if (!prefs.edit().putString(name, Base64.encodeToString(blob, Base64.NO_WRAP)).commit()) {
      throw new IllegalStateException("failed to persist secret");
    }
  }

  @Nullable
  public synchronized String get(String name) throws Exception {
    String stored = prefs.getString(name, null);
    if (stored == null) {
      return null;
    }
    byte[] blob = Base64.decode(stored, Base64.NO_WRAP);
    if (blob.length <= IV_BYTES) {
      return null;
    }
    try {
      Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
      cipher.init(
          Cipher.DECRYPT_MODE, getOrCreateKey(), new GCMParameterSpec(TAG_BITS, blob, 0, IV_BYTES));
      cipher.updateAAD(name.getBytes(StandardCharsets.UTF_8));
      byte[] pt = cipher.doFinal(blob, IV_BYTES, blob.length - IV_BYTES);
      return new String(pt, StandardCharsets.UTF_8);
    } catch (javax.crypto.AEADBadTagException | java.security.InvalidKeyException e) {
      // Restored from another device or the key was reset: the secret is unrecoverable.
      remove(name);
      return null;
    }
  }

  public synchronized void remove(String name) {
    prefs.edit().remove(name).commit();
  }
}
