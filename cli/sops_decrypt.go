package main

import (
	"bufio"
	"bytes"
	cryptoaes "crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
	"gopkg.in/yaml.v3"
)

const (
	sopsMetadataKey = "sops"
)

var (
	errSopsMetadataNotFound       = errors.New("sops metadata not found")
	errUnsupportedSopsAgeIdentity = errors.New("unsupported SOPS age identity")
	errMissingSopsAgeIdentity     = errors.New("missing SOPS age identity")
	errMissingSopsAgeKey          = errors.New("missing SOPS age encrypted key")
	errMissingSopsMAC             = errors.New("missing SOPS mac")

	sopsEncryptedValueRE = regexp.MustCompile(`^ENC\[AES256_GCM,data:([^,]*),iv:([^,]*),tag:([^,]*),type:([^\]]*)\]$`)

	sopsMACOnlyEncryptedInitialization = []byte{0x8a, 0x3f, 0xd2, 0xad, 0x54, 0xce, 0x66, 0x52, 0x7b, 0x10, 0x34, 0xf3, 0xd1, 0x47, 0xbe, 0x0b, 0x0b, 0x97, 0x5b, 0x3b, 0xf4, 0x4f, 0x72, 0xc6, 0xfd, 0xad, 0xec, 0x81, 0x76, 0xf2, 0x7d, 0x69}
)

type sopsAgeKey struct {
	Recipient string
	Encrypted string
}

type sopsYAMLMetadata struct {
	LastModified      time.Time
	MAC               string
	UnencryptedSuffix string
	EncryptedSuffix   string
	UnencryptedRegex  string
	EncryptedRegex    string
	MACOnlyEncrypted  bool
	AgeKeys           []sopsAgeKey
}

type sopsEncryptedValue struct {
	Data     []byte
	IV       []byte
	Tag      []byte
	DataType string
}

func decryptSopsFile(path, ageKeyFile string) ([]byte, error) {
	ageKeyFile = strings.TrimSpace(ageKeyFile)
	if ageKeyFile == "" {
		return nil, fmt.Errorf("%w: noebs.sops_age_key_file", errMissingSopsAgeKeyFile)
	}
	if _, err := requiredExistingPath("SOPS age key", ageKeyFile); err != nil {
		return nil, err
	}
	identities, err := readSopsAgeIdentities(ageKeyFile)
	if err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read SOPS YAML %s: %w", path, err)
	}
	return decryptSopsYAML(payload, identities)
}

func readSopsAgeIdentities(path string) ([]age.Identity, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open SOPS age key: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	var identities []age.Identity
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "AGE-SECRET-KEY-1") {
			return nil, fmt.Errorf("%w: %s", errUnsupportedSopsAgeIdentity, path)
		}
		identity, err := age.ParseX25519Identity(line)
		if err != nil {
			return nil, fmt.Errorf("parse SOPS age identity: %w", err)
		}
		identities = append(identities, identity)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SOPS age key: %w", err)
	}
	if len(identities) == 0 {
		return nil, fmt.Errorf("%w: %s", errMissingSopsAgeIdentity, path)
	}
	return identities, nil
}

func decryptSopsYAML(payload []byte, identities []age.Identity) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("parse SOPS YAML: %w", err)
	}
	root, err := yamlDocumentRoot(&doc)
	if err != nil {
		return nil, err
	}
	metadataNode, err := sopsMetadataNode(root)
	if err != nil {
		return nil, err
	}
	metadata, err := parseSopsYAMLMetadata(metadataNode)
	if err != nil {
		return nil, err
	}
	dataKey, err := decryptSopsAgeDataKey(metadata, identities)
	if err != nil {
		return nil, err
	}
	hash := sha512.New()
	if metadata.MACOnlyEncrypted {
		_, _ = hash.Write(sopsMACOnlyEncryptedInitialization)
	}
	if err := decryptSopsNode(root, metadata, dataKey, nil, hash, true); err != nil {
		return nil, err
	}
	computedMAC := fmt.Sprintf("%X", hash.Sum(nil))
	originalMAC, err := decryptSopsEncryptedValue(metadata.MAC, dataKey, metadata.LastModified.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("decrypt SOPS mac: %w", err)
	}
	if originalMAC.Text != computedMAC {
		return nil, fmt.Errorf("SOPS mac mismatch")
	}
	removeSopsMetadataNode(root)
	plain, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("marshal decrypted SOPS YAML: %w", err)
	}
	return plain, nil
}

func yamlDocumentRoot(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, fmt.Errorf("SOPS YAML must contain one document")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("SOPS YAML document must be a mapping")
	}
	return root, nil
}

func sopsMetadataNode(root *yaml.Node) (*yaml.Node, error) {
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == sopsMetadataKey {
			return root.Content[i+1], nil
		}
	}
	return nil, errSopsMetadataNotFound
}

func removeSopsMetadataNode(root *yaml.Node) {
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == sopsMetadataKey {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			return
		}
	}
}

func parseSopsYAMLMetadata(node *yaml.Node) (sopsYAMLMetadata, error) {
	if node.Kind != yaml.MappingNode {
		return sopsYAMLMetadata{}, fmt.Errorf("SOPS metadata must be a mapping")
	}
	values := yamlMapping(node)
	lastModified, err := requiredYAMLString(values, "lastmodified")
	if err != nil {
		return sopsYAMLMetadata{}, err
	}
	parsedLastModified, err := time.Parse(time.RFC3339, lastModified)
	if err != nil {
		return sopsYAMLMetadata{}, fmt.Errorf("parse SOPS lastmodified: %w", err)
	}
	mac, err := requiredYAMLString(values, "mac")
	if err != nil {
		return sopsYAMLMetadata{}, err
	}
	if strings.TrimSpace(mac) == "" {
		return sopsYAMLMetadata{}, errMissingSopsMAC
	}
	ageKeys, err := parseSopsAgeKeys(values["age"])
	if err != nil {
		return sopsYAMLMetadata{}, err
	}
	return sopsYAMLMetadata{
		LastModified:      parsedLastModified,
		MAC:               mac,
		UnencryptedSuffix: optionalYAMLString(values, "unencrypted_suffix"),
		EncryptedSuffix:   optionalYAMLString(values, "encrypted_suffix"),
		UnencryptedRegex:  optionalYAMLString(values, "unencrypted_regex"),
		EncryptedRegex:    optionalYAMLString(values, "encrypted_regex"),
		MACOnlyEncrypted:  optionalYAMLBool(values, "mac_only_encrypted"),
		AgeKeys:           ageKeys,
	}, nil
}

func parseSopsAgeKeys(node *yaml.Node) ([]sopsAgeKey, error) {
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return nil, errMissingSopsAgeKey
	}
	keys := make([]sopsAgeKey, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("SOPS age key entry must be a mapping")
		}
		values := yamlMapping(item)
		recipient, err := requiredYAMLString(values, "recipient")
		if err != nil {
			return nil, err
		}
		encrypted, err := requiredYAMLString(values, "enc")
		if err != nil {
			return nil, err
		}
		keys = append(keys, sopsAgeKey{Recipient: recipient, Encrypted: encrypted})
	}
	return keys, nil
}

func yamlMapping(node *yaml.Node) map[string]*yaml.Node {
	values := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		values[node.Content[i].Value] = node.Content[i+1]
	}
	return values
}

func requiredYAMLString(values map[string]*yaml.Node, key string) (string, error) {
	node := values[key]
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("missing SOPS metadata field %s", key)
	}
	return node.Value, nil
}

func optionalYAMLString(values map[string]*yaml.Node, key string) string {
	node := values[key]
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

func optionalYAMLBool(values map[string]*yaml.Node, key string) bool {
	node := values[key]
	return node != nil && node.Kind == yaml.ScalarNode && strings.EqualFold(node.Value, "true")
}

func decryptSopsAgeDataKey(metadata sopsYAMLMetadata, identities []age.Identity) ([]byte, error) {
	for _, key := range metadata.AgeKeys {
		dataKey, err := decryptSopsAgeEncryptedKey(key, identities)
		if err == nil {
			return dataKey, nil
		}
	}
	return nil, fmt.Errorf("decrypt SOPS age data key: no configured identity matched")
}

func decryptSopsAgeEncryptedKey(key sopsAgeKey, identities []age.Identity) ([]byte, error) {
	if strings.TrimSpace(key.Recipient) == "" || strings.TrimSpace(key.Encrypted) == "" {
		return nil, errMissingSopsAgeKey
	}
	reader, err := age.Decrypt(armor.NewReader(strings.NewReader(key.Encrypted)), identities...)
	if err != nil {
		return nil, err
	}
	var dataKey bytes.Buffer
	if _, err := io.Copy(&dataKey, reader); err != nil {
		return nil, err
	}
	return dataKey.Bytes(), nil
}

func decryptSopsNode(node *yaml.Node, metadata sopsYAMLMetadata, dataKey []byte, path []string, hash io.Writer, topLevel bool) error {
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			if topLevel && key.Value == sopsMetadataKey {
				continue
			}
			if key.Kind != yaml.ScalarNode {
				return fmt.Errorf("SOPS YAML mapping key must be scalar")
			}
			if err := decryptSopsNode(value, metadata, dataKey, append(path, key.Value), hash, false); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if err := decryptSopsNode(child, metadata, dataKey, path, hash, false); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		return decryptSopsScalarNode(node, metadata, dataKey, path, hash)
	case yaml.AliasNode:
		return fmt.Errorf("SOPS YAML aliases are not supported")
	default:
		return nil
	}
	return nil
}

func decryptSopsScalarNode(node *yaml.Node, metadata sopsYAMLMetadata, dataKey []byte, path []string, hash io.Writer) error {
	encrypted, err := sopsPathEncrypted(path, metadata)
	if err != nil {
		return err
	}
	if encrypted {
		if node.Value == "" {
			value := sopsPlainValue{Text: "", MACText: "", YAMLTag: "!!str"}
			node.Tag = value.YAMLTag
			node.Value = value.Text
			writeSopsMACValue(hash, value)
			return nil
		}
		value, err := decryptSopsEncryptedValue(node.Value, dataKey, strings.Join(path, ":")+":")
		if err != nil {
			return fmt.Errorf("decrypt SOPS value %s: %w", strings.Join(path, "."), err)
		}
		node.Tag = value.YAMLTag
		node.Value = value.Text
		writeSopsMACValue(hash, value)
		return nil
	}
	if !metadata.MACOnlyEncrypted {
		value, err := plainSopsScalarValue(node)
		if err != nil {
			return err
		}
		writeSopsMACValue(hash, value)
	}
	return nil
}

func sopsPathEncrypted(path []string, metadata sopsYAMLMetadata) (bool, error) {
	encrypted := true
	if metadata.UnencryptedSuffix != "" {
		for _, value := range path {
			if strings.HasSuffix(value, metadata.UnencryptedSuffix) {
				encrypted = false
				break
			}
		}
	}
	if metadata.EncryptedSuffix != "" {
		encrypted = false
		for _, value := range path {
			if strings.HasSuffix(value, metadata.EncryptedSuffix) {
				encrypted = true
				break
			}
		}
	}
	if metadata.UnencryptedRegex != "" {
		for _, value := range path {
			matched, err := regexp.MatchString(metadata.UnencryptedRegex, value)
			if err != nil {
				return false, fmt.Errorf("compile SOPS unencrypted_regex: %w", err)
			}
			if matched {
				encrypted = false
				break
			}
		}
	}
	if metadata.EncryptedRegex != "" {
		encrypted = false
		for _, value := range path {
			matched, err := regexp.MatchString(metadata.EncryptedRegex, value)
			if err != nil {
				return false, fmt.Errorf("compile SOPS encrypted_regex: %w", err)
			}
			if matched {
				encrypted = true
				break
			}
		}
	}
	return encrypted, nil
}

type sopsPlainValue struct {
	Text    string
	MACText string
	YAMLTag string
}

func decryptSopsEncryptedValue(ciphertext string, dataKey []byte, additionalData string) (sopsPlainValue, error) {
	encrypted, err := parseSopsEncryptedValue(ciphertext)
	if err != nil {
		return sopsPlainValue{}, err
	}
	block, err := cryptoaes.NewCipher(dataKey)
	if err != nil {
		return sopsPlainValue{}, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, len(encrypted.IV))
	if err != nil {
		return sopsPlainValue{}, err
	}
	data := append(encrypted.Data, encrypted.Tag...)
	plaintext, err := gcm.Open(nil, encrypted.IV, data, []byte(additionalData))
	if err != nil {
		return sopsPlainValue{}, fmt.Errorf("AES_GCM decrypt failed: %w", err)
	}
	return sopsPlainValueForDecryptedBytes(plaintext, encrypted.DataType)
}

func parseSopsEncryptedValue(value string) (sopsEncryptedValue, error) {
	matches := sopsEncryptedValueRE.FindStringSubmatch(value)
	if matches == nil {
		return sopsEncryptedValue{}, fmt.Errorf("invalid SOPS encrypted value")
	}
	data, err := base64.StdEncoding.DecodeString(matches[1])
	if err != nil {
		return sopsEncryptedValue{}, fmt.Errorf("decode SOPS data: %w", err)
	}
	iv, err := base64.StdEncoding.DecodeString(matches[2])
	if err != nil {
		return sopsEncryptedValue{}, fmt.Errorf("decode SOPS iv: %w", err)
	}
	tag, err := base64.StdEncoding.DecodeString(matches[3])
	if err != nil {
		return sopsEncryptedValue{}, fmt.Errorf("decode SOPS tag: %w", err)
	}
	return sopsEncryptedValue{Data: data, IV: iv, Tag: tag, DataType: matches[4]}, nil
}

func sopsPlainValueForDecryptedBytes(plaintext []byte, dataType string) (sopsPlainValue, error) {
	text := string(plaintext)
	switch dataType {
	case "str":
		return sopsPlainValue{Text: text, MACText: text, YAMLTag: "!!str"}, nil
	case "int":
		parsed, err := strconv.Atoi(text)
		if err != nil {
			return sopsPlainValue{}, err
		}
		return sopsPlainValue{Text: strconv.Itoa(parsed), MACText: strconv.Itoa(parsed), YAMLTag: "!!int"}, nil
	case "float":
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return sopsPlainValue{}, err
		}
		normalized := strconv.FormatFloat(parsed, 'f', -1, 64)
		return sopsPlainValue{Text: normalized, MACText: normalized, YAMLTag: "!!float"}, nil
	case "bool":
		parsed, err := strconv.ParseBool(text)
		if err != nil {
			return sopsPlainValue{}, err
		}
		if parsed {
			return sopsPlainValue{Text: "true", MACText: "True", YAMLTag: "!!bool"}, nil
		}
		return sopsPlainValue{Text: "false", MACText: "False", YAMLTag: "!!bool"}, nil
	case "bytes":
		return sopsPlainValue{Text: base64.StdEncoding.EncodeToString(plaintext), MACText: string(plaintext), YAMLTag: "!!binary"}, nil
	default:
		return sopsPlainValue{}, fmt.Errorf("unsupported SOPS value type %s", dataType)
	}
}

func plainSopsScalarValue(node *yaml.Node) (sopsPlainValue, error) {
	switch node.Tag {
	case "!!str":
		return sopsPlainValue{Text: node.Value, MACText: node.Value, YAMLTag: node.Tag}, nil
	case "!!int":
		parsed, err := strconv.Atoi(node.Value)
		if err != nil {
			return sopsPlainValue{}, err
		}
		text := strconv.Itoa(parsed)
		return sopsPlainValue{Text: text, MACText: text, YAMLTag: node.Tag}, nil
	case "!!float":
		parsed, err := strconv.ParseFloat(node.Value, 64)
		if err != nil {
			return sopsPlainValue{}, err
		}
		text := strconv.FormatFloat(parsed, 'f', -1, 64)
		return sopsPlainValue{Text: text, MACText: text, YAMLTag: node.Tag}, nil
	case "!!bool":
		parsed, err := strconv.ParseBool(node.Value)
		if err != nil {
			return sopsPlainValue{}, err
		}
		if parsed {
			return sopsPlainValue{Text: "true", MACText: "True", YAMLTag: node.Tag}, nil
		}
		return sopsPlainValue{Text: "false", MACText: "False", YAMLTag: node.Tag}, nil
	default:
		return sopsPlainValue{}, fmt.Errorf("unsupported unencrypted SOPS YAML scalar type %s", node.Tag)
	}
}

func writeSopsMACValue(hash io.Writer, value sopsPlainValue) {
	_, _ = hash.Write([]byte(value.MACText))
}
