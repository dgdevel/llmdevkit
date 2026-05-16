package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"llmdevkit/internal/cfg"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func main() {
	useGlobal := false
	rootDir := "."

	args := os.Args[1:]
	for len(args) > 0 && args[0] == "--global" {
		useGlobal = true
		args = args[1:]
	}
	if len(args) > 0 {
		if useGlobal {
			fmt.Fprintln(os.Stderr, "error: cannot specify root directory with --global")
			os.Exit(1)
		}
		rootDir = args[0]
	}
	rootDir, _ = filepath.Abs(rootDir)

	var baseDir string
	if useGlobal {
		baseDir = cfg.GlobalDirPath()
		if baseDir == "" {
			fmt.Fprintln(os.Stderr, "error: cannot determine global config directory")
			os.Exit(1)
		}
	} else {
		baseDir = cfg.DirPath(rootDir)
	}

	llamaDir := filepath.Join(baseDir, "llama.cpp")
	modelsDir := filepath.Join(baseDir, "models")

	llamaServerPath, err := setupLlamaCpp(llamaDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("llama-server: %s\n", llamaServerPath)

	embedderRepo := "nomic-ai/nomic-embed-text-v1.5-GGUF"
	embedderFile := "nomic-embed-text-v1.5.Q4_K_M.gguf"
	if err := downloadHFModel(modelsDir, embedderRepo, embedderFile); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	rerankerRepo := "xinming0111/bge-reranker-base-Q8_0-GGUF"
	rerankerFile := "bge-reranker-base-q8_0.gguf"
	if err := downloadHFModel(modelsDir, rerankerRepo, rerankerFile); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	extractorRepo := "unsloth/Qwen3.5-0.8B-GGUF"
	extractorFile := "Qwen3.5-0.8B-UD-Q4_K_XL.gguf"
	if err := downloadHFModel(modelsDir, extractorRepo, extractorFile); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var configPath string
	if useGlobal {
		configPath = cfg.GlobalFilePath()
	} else {
		configPath = cfg.FilePath(rootDir)
	}
	config, err := cfg.Read(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if config["llama"] == nil {
		config["llama"] = map[string]string{}
	}
	config["llama"]["path"] = llamaServerPath
	config["llama"]["embedder"] = embedderRepo
	config["llama"]["reranker"] = rerankerRepo
	config["llama"]["extractor"] = extractorRepo
	config["llama"]["embedder_flags"] = "--threads 1 --threads-batch 1"
	config["llama"]["extractor_flags"] = "--temp 0 --ctx-size 262144"
	if err := cfg.Write(config, configPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Config updated.")
}

func httpGet(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "llmdevkit-setup")
	return http.DefaultClient.Do(req)
}

func setupLlamaCpp(dir string) (string, error) {
	fmt.Println("Checking llama.cpp latest release...")
	release, err := fetchLatestRelease("ggml-org/llama.cpp")
	if err != nil {
		return "", fmt.Errorf("fetching latest release: %w", err)
	}

	versionFile := filepath.Join(dir, ".version")
	currentVersion, _ := os.ReadFile(versionFile)
	if strings.TrimSpace(string(currentVersion)) == release.TagName {
		serverPath := findExecutable(dir, "llama-server")
		if serverPath != "" {
			fmt.Println("llama.cpp is up to date.")
			return serverPath, nil
		}
	}

	var assetURL, assetName string
	for _, a := range release.Assets {
		if !strings.Contains(a.Name, "-bin-ubuntu-x64.") {
			continue
		}
		if strings.Contains(a.Name, "cuda") || strings.Contains(a.Name, "rocm") ||
			strings.Contains(a.Name, "vulkan") || strings.Contains(a.Name, "sycl") ||
			strings.Contains(a.Name, "openvino") {
			continue
		}
		assetURL = a.BrowserDownloadURL
		assetName = a.Name
		break
	}
	if assetURL == "" {
		return "", fmt.Errorf("no ubuntu-x64 CPU asset found in release %s", release.TagName)
	}

	fmt.Printf("Downloading %s...\n", assetName)
	resp, err := httpGet(assetURL)
	if err != nil {
		return "", fmt.Errorf("downloading: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}

	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating directory: %w", err)
	}

	if strings.HasSuffix(assetName, ".tar.gz") {
		if err := extractTarGz(resp.Body, dir); err != nil {
			return "", fmt.Errorf("extracting: %w", err)
		}
	} else {
		return "", fmt.Errorf("unsupported archive format: %s", assetName)
	}

	os.WriteFile(versionFile, []byte(release.TagName), 0644)
	makeBinariesExecutable(dir)
	createSoSymlinks(dir)

	serverPath := findExecutable(dir, "llama-server")
	if serverPath == "" {
		return "", fmt.Errorf("llama-server not found in archive")
	}
	return serverPath, nil
}

func fetchLatestRelease(repo string) (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	resp, err := httpGet(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

func extractTarGz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		parts := strings.SplitN(header.Name, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		target := filepath.Join(dest, parts[1])
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

func makeBinariesExecutable(dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Mode()&0111 != 0 {
			os.Chmod(path, 0755)
		}
		return nil
	})
}

func findExecutable(dir, name string) string {
	var result string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Name() == name {
			result = path
		}
		return nil
	})
	return result
}

func downloadHFModel(dir, repoID, filename string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	targetPath := filepath.Join(dir, filename)

	sha, err := fetchHFModelSha(repoID)
	if err == nil && sha != "" {
		existingSha, _ := os.ReadFile(targetPath + ".sha")
		if strings.TrimSpace(string(existingSha)) == sha {
			if info, err := os.Stat(targetPath); err == nil && info.Size() > 0 {
				fmt.Printf("%s is up to date.\n", filename)
				return nil
			}
		}
	}

	url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repoID, filename)
	fmt.Printf("Downloading %s...\n", filename)
	resp, err := httpGet(url)
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	tmpPath := targetPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	f.Close()
	if err := os.Rename(tmpPath, targetPath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if sha != "" {
		os.WriteFile(targetPath+".sha", []byte(sha), 0644)
	}
	return nil
}

func fetchHFModelSha(repoID string) (string, error) {
	url := fmt.Sprintf("https://huggingface.co/api/models/%s", repoID)
	resp, err := httpGet(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HuggingFace API returned %s", resp.Status)
	}
	var info struct {
		Sha string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	return info.Sha, nil
}

var soPattern = regexp.MustCompile(`^(.+\.so)\.\d+\.\d+\.\d+$`)

func createSoSymlinks(dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		m := soPattern.FindStringSubmatch(name)
		if m == nil {
			return nil
		}

		major := m[1]
		parts := strings.SplitN(name[len(m[1])+1:], ".", 2)
		if len(parts) > 0 && parts[0] != "" {
			major = m[1] + "." + parts[0]
		}

		linkPath := filepath.Join(filepath.Dir(path), major)
		os.Remove(linkPath)
		if err := os.Symlink(name, linkPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: symlink %s -> %s: %v\n", major, name, err)
		} else {
			fmt.Printf("  symlink: %s -> %s\n", major, name)
		}
		return nil
	})
}
