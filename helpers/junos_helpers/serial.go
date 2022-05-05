package junos_helpers

import (
	"encoding/xml"
	"fmt"
	"log"
	"strings"
	"sync"

	driver "github.com/davedotdev/go-netconf/drivers/driver"
	sshdriver "github.com/davedotdev/go-netconf/drivers/ssh"

	"golang.org/x/crypto/ssh"
)

const groupStrXML = `<load-configuration action="merge" format="xml">
%s
</load-configuration>
`

const deleteStr = `<edit-config>
	<target>
		<candidate/>
	</target>
	<default-operation>none</default-operation> 
	<config>
		<configuration>
			<groups operation="delete">
				<name>%s</name>
			</groups>
			<apply-groups operation="delete">%s</apply-groups>
		</configuration>
	</config>
</edit-config>`

const commitStr = `<commit/>`

const getGroupStr = `<get-configuration database="committed" format="text" >
  <configuration>
  <groups><name>%s</name></groups>
  </configuration>
</get-configuration>
`

const getGroupXMLStr = `<get-configuration>
  <configuration>
  <groups><name>%s</name></groups>
  </configuration>
</get-configuration>
`

// GoNCClient type for storing data and wrapping functions
type GoNCClient struct {
	Driver driver.Driver
	Lock   sync.RWMutex
}

// Close is a functional thing to close the Driver
func (g *GoNCClient) Close() error {
	g.Driver = nil
	return nil
}

// ReadGroup is a helper function
func (g *GoNCClient) ReadGroup(applygroup string) (string, error) {
	g.Lock.Lock()
	err := g.Driver.Dial()

	if err != nil {
		log.Fatal(err)
	}

	getGroupString := fmt.Sprintf(getGroupStr, applygroup)

	reply, err := g.Driver.SendRaw(getGroupString)
	if err != nil {
		return "", err
	}

	err = g.Driver.Close()

	g.Lock.Unlock()

	if err != nil {
		return "", err
	}

	parsedGroupData, err := parseGroupData(reply.Data)
	if err != nil {
		return "", err
	}

	return parsedGroupData, nil
}

// UpdateRawConfig deletes group data and replaces it (for Update in TF)
func (g *GoNCClient) UpdateRawConfig(applygroup string, netconfcall string, commit bool) (string, error) {

	deleteString := fmt.Sprintf(deleteStr, applygroup, applygroup)

	g.Lock.Lock()
	err := g.Driver.Dial()
	if err != nil {
		log.Fatal(err)
	}

	_, err = g.Driver.SendRaw(deleteString)
	if err != nil {
		errInternal := g.Driver.Close()
		g.Lock.Unlock()
		return "", fmt.Errorf("driver error: %+v, driver close error: %+s", err, errInternal)
	}

	groupString := fmt.Sprintf(groupStrXML, netconfcall)

	reply, err := g.Driver.SendRaw(groupString)
	if err != nil {
		errInternal := g.Driver.Close()
		g.Lock.Unlock()
		return "", fmt.Errorf("driver error: %+v, driver close error: %+s", err, errInternal)
	}

	if commit {
		_, err = g.Driver.SendRaw(commitStr)
		if err != nil {
			errInternal := g.Driver.Close()
			g.Lock.Unlock()
			return "", fmt.Errorf("driver error: %+v, driver close error: %+s", err, errInternal)
		}
	}

	err = g.Driver.Close()

	if err != nil {
		g.Lock.Unlock()
		return "", fmt.Errorf("driver close error: %+s", err)
	}

	g.Lock.Unlock()

	return reply.Data, nil
}

// DeleteConfig is a wrapper for driver.SendRaw()
func (g *GoNCClient) DeleteConfig(applygroup string) (string, error) {

	deleteString := fmt.Sprintf(deleteStr, applygroup, applygroup)

	g.Lock.Lock()
	err := g.Driver.Dial()
	if err != nil {
		log.Fatal(err)
	}

	reply, err := g.Driver.SendRaw(deleteString)
	if err != nil {
		errInternal := g.Driver.Close()
		g.Lock.Unlock()
		return "", fmt.Errorf("driver error: %+v, driver close error: %+s", err, errInternal)
	}

	_, err = g.Driver.SendRaw(commitStr)
	if err != nil {
		errInternal := g.Driver.Close()
		g.Lock.Unlock()
		return "", fmt.Errorf("driver error: %+v, driver close error: %+s", err, errInternal)
	}

	output := strings.Replace(reply.Data, "\n", "", -1)

	err = g.Driver.Close()

	g.Lock.Unlock()

	if err != nil {
		log.Fatal(err)
	}

	return output, nil
}

// DeleteConfigNoCommit is a wrapper for driver.SendRaw()
// Does not provide mandatory commit unlike DeleteConfig()
func (g *GoNCClient) DeleteConfigNoCommit(applygroup string) (string, error) {

	deleteString := fmt.Sprintf(deleteStr, applygroup, applygroup)

	g.Lock.Lock()
	err := g.Driver.Dial()
	if err != nil {
		log.Fatal(err)
	}

	reply, err := g.Driver.SendRaw(deleteString)
	if err != nil {
		errInternal := g.Driver.Close()
		g.Lock.Unlock()
		return "", fmt.Errorf("driver error: %+v, driver close error: %+s", err, errInternal)
	}

	output := strings.Replace(reply.Data, "\n", "", -1)

	err = g.Driver.Close()

	if err != nil {
		g.Lock.Unlock()
		return "", fmt.Errorf("driver close error: %+s", err)
	}

	g.Lock.Unlock()

	return output, nil
}

// SendCommit is a wrapper for driver.SendRaw()
func (g *GoNCClient) SendCommit() error {
	g.Lock.Lock()

	err := g.Driver.Dial()

	if err != nil {
		g.Lock.Unlock()
		return err
	}

	_, err = g.Driver.SendRaw(commitStr)
	if err != nil {
		g.Lock.Unlock()
		return err
	}

	g.Lock.Unlock()
	return nil
}

// MarshalGroup accepts a struct of type X and then marshals data onto it
func (g *GoNCClient) MarshalGroup(id string, obj interface{}) error {

	reply, err := g.ReadRawGroup(id)
	if err != nil {
		return err
	}

	err = xml.Unmarshal([]byte(reply), &obj)
	if err != nil {
		return err
	}
	return nil
}

// SendTransaction is a method that unnmarshals the XML, creates the transaction and passes in a commit
func (g *GoNCClient) SendTransaction(id string, obj interface{}, commit bool) error {
	jconfig, err := xml.Marshal(obj)

	if err != nil {
		return err
	}

	// UpdateRawConfig deletes old group by, re-creates it then commits.
	// As far as Junos cares, it's an edit.
	if id != "" {
		_, err = g.UpdateRawConfig(id, string(jconfig), commit)
	} else {
		_, err = g.SendRawConfig(string(jconfig), commit)
	}

	if err != nil {
		return err
	}
	return nil
}

// SendRawConfig is a wrapper for driver.SendRaw()
func (g *GoNCClient) SendRawConfig(netconfcall string, commit bool) (string, error) {

	groupString := fmt.Sprintf(groupStrXML, netconfcall)

	g.Lock.Lock()

	err := g.Driver.Dial()

	if err != nil {
		log.Fatal(err)
	}

	reply, err := g.Driver.SendRaw(groupString)
	if err != nil {
		errInternal := g.Driver.Close()
		g.Lock.Unlock()
		return "", fmt.Errorf("driver error: %+v, driver close error: %+s", err, errInternal)
	}

	if commit {
		_, err = g.Driver.SendRaw(commitStr)
		if err != nil {
			errInternal := g.Driver.Close()
			g.Lock.Unlock()
			return "", fmt.Errorf("driver error: %+v, driver close error: %+s", err, errInternal)
		}
	}

	err = g.Driver.Close()

	if err != nil {
		g.Lock.Unlock()
		return "", err
	}

	g.Lock.Unlock()

	return reply.Data, nil
}

// SendRawNetconfConfig - This is meant for sending a raw NETCONF strings without any wrapping around the input
func (g *GoNCClient) SendRawNetconfConfig(netconfcall string) (string, error) {

	g.Lock.Lock()
	defer g.Lock.Unlock()

	if err := g.Driver.Dial(); err != nil {
		return "", err
	}

	reply, err := g.Driver.SendRaw(netconfcall)
	if err != nil {
		errInternal := g.Driver.Close()
		g.Lock.Unlock()
		return "", fmt.Errorf("driver error: %+v, driver close error: %+s", err, errInternal)
	}

	if err = g.Driver.Close(); err != nil {
		return "", err
	}

	return reply.Data, nil
}

// ReadRawGroup is a helper function
func (g *GoNCClient) ReadRawGroup(applygroup string) (string, error) {
	g.Lock.Lock()
	err := g.Driver.Dial()

	if err != nil {
		log.Fatal(err)
	}

	getGroupXMLString := fmt.Sprintf(getGroupXMLStr, applygroup)

	reply, err := g.Driver.SendRaw(getGroupXMLString)
	if err != nil {
		errInternal := g.Driver.Close()
		g.Lock.Unlock()
		return "", fmt.Errorf("driver error: %+v, driver close error: %+s", err, errInternal)
	}

	err = g.Driver.Close()

	g.Lock.Unlock()

	if err != nil {
		return "", err
	}

	return reply.Data, nil
}

// NewSerialClient returns gonetconf new client driver
func NewSerialClient(username string, password string, sshkey string, address string, port int) (NCClient, error) {

	// Dummy interface var ready for loading from inputs
	var nconf driver.Driver

	d := driver.New(sshdriver.New())

	nc := d.(*sshdriver.DriverSSH)

	nc.Host = address
	nc.Port = port

	// SSH keys takes priority over password based
	if sshkey != "" {
		nc.SSHConfig = &ssh.ClientConfig{
			User: username,
			Auth: []ssh.AuthMethod{
				publicKeyFile(sshkey),
			},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		}
	} else {
		// Sort yourself out with SSH. Easiest to do that here.
		nc.SSHConfig = &ssh.ClientConfig{
			User:            username,
			Auth:            []ssh.AuthMethod{ssh.Password(password)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		}
	}

	nconf = nc

	return &GoNCClient{Driver: nconf}, nil
}

//Deprecated - NewClient has been superseded by NewSerialClient / NewBatchClient respecitvly. The return type is now an interface
// that will allow for greater flexibility going forward.
func NewClient(username string, password string, sshkey string, address string, port int) (*GoNCClient, error) {
	serial, err := NewSerialClient(username, password, sshkey, address, port)
	if err != nil {
		return nil, err
	}
	out := serial.(*GoNCClient)
	return out, nil
}