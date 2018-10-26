package cafe

import (
	"archive/tar"
	"compress/gzip"
	njwt "github.com/dgrijalva/jwt-go"
	"github.com/gin-gonic/gin"
	"github.com/textileio/textile-go/ipfs"
	"github.com/textileio/textile-go/jwt"
	"gx/ipfs/QmYVNvtQkeZ6AKSwDrjQTs432QtL6umrrK41EBq3cu7iSP/go-cid"
	uio "gx/ipfs/QmebqVUQQqQFhg74FtQFszUJo22Vpr3e8qBAkvvV4ho9HH/go-ipfs/unixfs/io"
	"io"
	"net/http"
	"strings"
)

type PinResponse struct {
	Id    *string `json:"id,omitempty"`
	Error *string `json:"error,omitempty"`
}

var errForbidden = "forbidden"
var forbiddenResponse = PinResponse{
	Error: &errForbidden,
}
var ErrUnauthorized = "unauthorized"
var unauthorizedResponse = PinResponse{
	Error: &ErrUnauthorized,
}

func (c *Cafe) pin(g *gin.Context) {
	var id *cid.Cid

	// get the auth token
	auth := strings.Split(g.Request.Header.Get("Authorization"), " ")
	if len(auth) < 2 {
		g.AbortWithStatusJSON(http.StatusUnauthorized, unauthorizedResponse)
		return
	}
	token := auth[1]

	// validate token
	if err := jwt.Validate(token, c.verifyKeyFunc, false, string(c.Protocol), nil); err != nil {
		switch err {
		case jwt.ErrNoToken, jwt.ErrExpired:
			g.AbortWithStatusJSON(http.StatusUnauthorized, unauthorizedResponse)
		case jwt.ErrInvalid:
			g.AbortWithStatusJSON(http.StatusForbidden, forbiddenResponse)
		}
		return
	}

	// handle based on content type
	cType := g.Request.Header.Get("Content-Type")
	switch cType {
	case "application/gzip":
		// create a virtual directory for the photo
		dirb := uio.NewDirectory(c.Ipfs().DAG)
		// unpack archive
		gr, err := gzip.NewReader(g.Request.Body)
		if err != nil {
			log.Errorf("error creating gzip reader %s", err)
			g.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		tr := tar.NewReader(gr)
		for {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Errorf("error getting tar next %s", err)
				g.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			switch header.Typeflag {
			case tar.TypeDir:
				log.Error("got nested directory, aborting")
				g.JSON(http.StatusBadRequest, gin.H{"error": "directories are not supported"})
				return
			case tar.TypeReg:
				if _, err := ipfs.AddFileToDirectory(c.Ipfs(), dirb, tr, header.Name); err != nil {
					log.Errorf("error adding file to dir %s", err)
					g.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
			default:
				continue
			}
		}

		// pin the directory
		dir, err := dirb.GetNode()
		if err != nil {
			log.Errorf("error creating dir node %s", err)
			g.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := ipfs.PinDirectory(c.Ipfs(), dir, []string{}); err != nil {
			log.Errorf("error pinning dir node %s", err)
			g.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		id = dir.Cid()

	case "application/octet-stream":
		var err error
		id, err = ipfs.PinData(c.Ipfs(), g.Request.Body)
		if err != nil {
			log.Errorf("error pinning raw body %s", err)
			g.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	default:
		log.Errorf("got bad content type %s", cType)
		g.JSON(http.StatusBadRequest, gin.H{"error": "invalid content-type"})
		return
	}
	hash := id.Hash().B58String()

	log.Debugf("pinned request with content type %s: %s", cType, hash)

	// ship it
	g.JSON(http.StatusCreated, PinResponse{
		Id: &hash,
	})
}

// verifyKeyFunc returns the correct key for token verification
func (c *Cafe) verifyKeyFunc(token *njwt.Token) (interface{}, error) {
	return c.Ipfs().PrivateKey.GetPublic(), nil
}
