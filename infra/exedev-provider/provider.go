package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

const gib = 1 << 30

type client struct {
	token, endpoint string
	http            *http.Client
}

// The API is the documented exe.dev CLI in a POST body. Tokens never enter state.
func (c *client) exec(ctx context.Context, args ...string) ([]byte, error) {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(strings.Join(quoted, " ")))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "text/plain")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exe.dev %s request: %w", args[0], err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("exe.dev %s returned HTTP %d: %s", args[0], resp.StatusCode, strings.ReplaceAll(string(body), c.token, "[redacted]"))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

type vm struct {
	Name       string `json:"vm_name"`
	Image      string `json:"image"`
	CPU        int    `json:"allocated_cpus"`
	Memory     int    `json:"memory_capacity_bytes"`
	Disk       int    `json:"disk_capacity_bytes"`
	SSH        string `json:"ssh_dest"`
	Region     string `json:"region"`
	Visibility string `json:"proxy_share"`
}

func (c *client) lookup(ctx context.Context, name string) (*vm, error) {
	body, err := c.exec(ctx, "ls", "--json")
	if err != nil {
		return nil, err
	}
	var result struct {
		VMs *[]vm `json:"vms"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("invalid exe.dev inventory JSON: %w", err)
	}
	if result.VMs == nil {
		return nil, fmt.Errorf("exe.dev inventory is missing vms")
	}
	for _, item := range *result.VMs {
		if item.Name == name {
			if item.CPU < 1 || item.Memory < gib || item.Disk < gib || item.SSH == "" || item.Image == "" {
				return nil, fmt.Errorf("exe.dev VM %q has incomplete capacity or identity", name)
			}
			return &item, nil
		}
	}
	return nil, nil
}

func provider() *schema.Provider {
	return &schema.Provider{
		ResourcesMap: map[string]*schema.Resource{"exedev_vm": vmResource()},
		ConfigureContextFunc: func(_ context.Context, _ *schema.ResourceData) (interface{}, diag.Diagnostics) {
			token := os.Getenv("EXEDEV_TOKEN")
			if strings.TrimSpace(token) == "" {
				return nil, diag.Errorf("EXEDEV_TOKEN must be provided")
			}
			return &client{token: token, endpoint: "https://exe.dev/exec", http: &http.Client{Timeout: 2 * time.Minute}}, nil
		},
	}
}

func vmResource() *schema.Resource {
	return &schema.Resource{
		CreateContext: createVM, ReadContext: readVM, UpdateContext: updateVM, DeleteContext: deleteVM,
		Importer: &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},
		CustomizeDiff: func(_ context.Context, d *schema.ResourceDiff, _ interface{}) error {
			old, next := d.GetChange("disk_gib")
			if d.Id() != "" && next.(int) < old.(int) {
				return fmt.Errorf("exe.dev disks cannot shrink; retain disk_gib >= %d", old.(int))
			}
			return nil
		},
		Schema: map[string]*schema.Schema{
			"name": {Type: schema.TypeString, Required: true, ForceNew: true, ValidateFunc: validation.StringMatch(regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`), "must be a lowercase VM name")},
			"image": {Type: schema.TypeString, Required: true, ForceNew: true, ValidateFunc: validation.StringIsNotWhiteSpace,
				// Inventory drops registry hosts and abbreviates digests to eight hex characters.
				DiffSuppressFunc: func(_ string, old, next string, _ *schema.ResourceData) bool {
					if host, path, ok := strings.Cut(next, "/"); ok && strings.ContainsAny(host, ".:") {
						next = path
					}
					parts := strings.Split(old, "@sha256:")
					return len(parts) == 2 && len(parts[1]) == 8 && strings.HasPrefix(next, old) && len(next) == len(old)+56
				}},
			"cpus":            {Type: schema.TypeInt, Required: true, ValidateFunc: validation.IntAtLeast(1)},
			"memory_gib":      {Type: schema.TypeInt, Required: true, ValidateFunc: validation.IntAtLeast(1)},
			"disk_gib":        {Type: schema.TypeInt, Required: true, ValidateFunc: validation.IntAtLeast(1)},
			"ssh_destination": {Type: schema.TypeString, Computed: true},
			"region":          {Type: schema.TypeString, Computed: true},
			"private":         {Type: schema.TypeBool, Required: true},
		},
	}
}

func createVM(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	c := meta.(*client)
	name := d.Get("name").(string)
	existing, err := c.lookup(ctx, name)
	if err != nil {
		return diag.FromErr(err)
	}
	if existing != nil {
		return diag.Errorf("VM %q already exists; import it before applying", name)
	}
	_, createErr := c.exec(ctx, "new", "--json", "--no-email", "--name="+name, "--image="+d.Get("image").(string),
		fmt.Sprintf("--cpu=%d", d.Get("cpus")), fmt.Sprintf("--memory=%dGB", d.Get("memory_gib")), fmt.Sprintf("--disk=%dGB", d.Get("disk_gib")))
	// A timed-out mutation may have succeeded. Record a confirmed VM before reporting failure.
	created, readErr := c.lookup(ctx, name)
	if created != nil {
		d.SetId(name)
	}
	if createErr != nil {
		return diag.FromErr(createErr)
	}
	if readErr != nil {
		return diag.FromErr(readErr)
	}
	if created == nil {
		return diag.Errorf("exe.dev create completed without VM %q", name)
	}
	if err := setVisibility(ctx, c, name, d.Get("private").(bool)); err != nil {
		return diag.FromErr(err)
	}
	return readVM(ctx, d, meta)
}

func readVM(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	item, err := meta.(*client).lookup(ctx, d.Id())
	if err != nil {
		return diag.FromErr(err)
	}
	if item == nil {
		d.SetId("")
		return nil
	}
	if item.Memory%gib != 0 || item.Disk%gib != 0 {
		return diag.Errorf("VM capacity is not an integral GiB")
	}
	if item.Visibility != "private" && item.Visibility != "public" {
		return diag.Errorf("VM has unknown proxy visibility %q", item.Visibility)
	}
	for key, value := range map[string]interface{}{
		"name": item.Name, "image": item.Image, "cpus": item.CPU, "memory_gib": item.Memory / gib,
		"disk_gib": item.Disk / gib, "ssh_destination": item.SSH, "region": item.Region, "private": item.Visibility == "private",
	} {
		if err := d.Set(key, value); err != nil {
			return diag.FromErr(err)
		}
	}
	return nil
}

func updateVM(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	c := meta.(*client)
	args := []string{"resize", d.Id(), "--json"}
	for _, field := range []struct{ key, flag, unit string }{{"cpus", "cpu", ""}, {"memory_gib", "memory", "GB"}, {"disk_gib", "disk", "GB"}} {
		if d.HasChange(field.key) {
			args = append(args, fmt.Sprintf("--%s=%d%s", field.flag, d.Get(field.key), field.unit))
		}
	}
	if len(args) > 3 {
		if _, err := c.exec(ctx, args...); err != nil {
			return diag.FromErr(err)
		}
	}
	if d.HasChange("private") {
		if err := setVisibility(ctx, c, d.Id(), d.Get("private").(bool)); err != nil {
			return diag.FromErr(err)
		}
	}
	return readVM(ctx, d, meta)
}

func setVisibility(ctx context.Context, c *client, name string, private bool) error {
	visibility := "set-public"
	if private {
		visibility = "set-private"
	}
	_, err := c.exec(ctx, "share", visibility, name, "--json")
	return err
}

func deleteVM(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	c := meta.(*client)
	item, err := c.lookup(ctx, d.Id())
	if err != nil {
		return diag.FromErr(err)
	}
	if item == nil {
		d.SetId("")
		return nil
	}
	if _, err := c.exec(ctx, "rm", d.Id(), "--json"); err != nil {
		return diag.FromErr(err)
	}
	item, err = c.lookup(ctx, d.Id())
	if err != nil {
		return diag.FromErr(err)
	}
	if item != nil {
		return diag.Errorf("exe.dev delete returned but VM %q still exists", d.Id())
	}
	d.SetId("")
	return nil
}
