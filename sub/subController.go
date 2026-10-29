package sub

import (
	"encoding/base64"
	"errors"
	"net"
	"strings"

	"github.com/gin-gonic/gin"
)

type SUBController struct {
	subPath        string
	subJsonPath    string
	subEncrypt     bool
	updateInterval string

	subService     *SubService
	subJsonService *SubJsonService
}

func NewSUBController(
	g *gin.RouterGroup,
	subPath string,
	jsonPath string,
	encrypt bool,
	showInfo bool,
	rModel string,
	update string,
	jsonMux string,
	jsonRules string,
) *SUBController {
	sub := NewSubService(showInfo, rModel)
	a := &SUBController{
		subPath:        subPath,
		subJsonPath:    jsonPath,
		subEncrypt:     encrypt,
		updateInterval: update,

		subService:     sub,
		subJsonService: NewSubJsonService(jsonMux, jsonRules, sub),
	}
	a.initRouter(g)
	return a
}

func (a *SUBController) initRouter(g *gin.RouterGroup) {
	gLink := g.Group(a.subPath)
	gJson := g.Group(a.subJsonPath)

	gLink.GET(":subid", a.subs)
	gLink.GET(":subid/singbox", a.singbox)
	gLink.GET(":subid/mihomo", a.mihomo)

	gJson.GET(":subid", a.subJsons)
}

func (a *SUBController) subs(c *gin.Context) {
	subId := c.Param("subid")
	host := subscriptionHost(c.Request.Host)
	subs, header, err := a.subService.GetSubs(subId, host)
	if err != nil || len(subs) == 0 {
		c.String(400, "Error!")
	} else {
		result := ""
		for _, sub := range subs {
			result += sub + "\n"
		}

		// Add headers
		c.Writer.Header().Set("Subscription-Userinfo", header)
		c.Writer.Header().Set("Profile-Update-Interval", a.updateInterval)
		c.Writer.Header().Set("Profile-Title", subId)

		if a.subEncrypt {
			c.String(200, base64.StdEncoding.EncodeToString([]byte(result)))
		} else {
			c.String(200, result)
		}
	}
}

// singbox serves a standalone client configuration for the same subscription ID.
func (a *SUBController) singbox(c *gin.Context) {
	links, header, err := a.subService.getSubs(c.Param("subid"), subscriptionHost(c.Request.Host), true)
	if err != nil {
		c.String(422, "Cannot generate sing-box configuration: %s", err.Error())
		return
	}
	if len(links) == 0 {
		c.String(400, "No subscription nodes")
		return
	}
	data, err := singboxConfig(links)
	if err != nil {
		c.String(422, "Cannot generate sing-box configuration: %s", err.Error())
		return
	}
	c.Header("Subscription-Userinfo", header)
	c.Header("Profile-Update-Interval", a.updateInterval)
	c.Header("Cache-Control", "no-store")
	c.Data(200, "application/json; charset=utf-8", data)
}

func (a *SUBController) mihomo(c *gin.Context) {
	links, header, err := a.subService.getSubs(c.Param("subid"), subscriptionHost(c.Request.Host), true)
	if err != nil {
		c.String(422, "Cannot generate Mihomo configuration: %s", err.Error())
		return
	}
	if len(links) == 0 {
		c.String(400, "No subscription nodes")
		return
	}
	data, err := mihomoConfig(links)
	if err != nil {
		c.String(422, "Cannot generate Mihomo configuration: %s", err.Error())
		return
	}
	c.Header("Subscription-Userinfo", header)
	c.Header("Profile-Update-Interval", a.updateInterval)
	c.Header("Cache-Control", "no-store")
	c.Data(200, "application/yaml; charset=utf-8", data)
}

func (a *SUBController) subJsons(c *gin.Context) {
	subId := c.Param("subid")
	host := subscriptionHost(c.Request.Host)
	jsonSub, header, err := a.subJsonService.GetJson(subId, host)
	if err != nil {
		if errors.Is(err, errXrayInsecureTLS) {
			c.String(400, "Xray no longer supports allowInsecure; configure pinnedPeerCertSha256")
		} else {
			c.String(400, "Error!")
		}
	} else if len(jsonSub) == 0 {
		c.String(400, "Error!")
	} else {

		// Add headers
		c.Writer.Header().Set("Subscription-Userinfo", header)
		c.Writer.Header().Set("Profile-Update-Interval", a.updateInterval)
		c.Writer.Header().Set("Profile-Title", subId)

		c.String(200, jsonSub)
	}
}

// subscriptionHost accepts hostnames, host:port, and IPv6 literals with or without brackets.
func subscriptionHost(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}
	return strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
}
