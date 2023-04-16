package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/pkg/term"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

type mailInfo struct {
	From, To, Subject, Date string
	Size, Attachments       int
	AttachmentNames         []string
}

// ./imap -s imap.mail.ru:993 -u <email> --ssl
func main() {
	help := flag.Bool("h", false, "help")
	ssl := flag.Bool("ssl", false, "allow ssl if server supports it (by default do not use it).")
	server := flag.String("s", "", "address (or domain name) of IMAP server in address[:port] format (default port is 143).")
	numRange := flag.String("n", "", "range of mails, all by default.")
	user := flag.String("u", "", "username, ask for password after launching and don't show it on the screen.")
	flag.Parse()

	if *help || *server == "" || *user == "" {
		flag.Usage()
		os.Exit(1)
	}

	serverHost, serverPort := parseServer(*server)

	fmt.Println("Parsed server: ", serverHost, serverPort)

	password := getPassword()

	fmt.Println("Got your password, connecting...")

	imapClient, err := connect(serverHost, serverPort, *ssl, *user, password)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	fmt.Println("Connected to server")

	defer func() {
		if err := imapClient.Logout(); err != nil {
			log.Printf("Failed to logout: %v", err)
		}
	}()

	fmt.Println("Fetching mails...")

	mails, err := fetchMails(imapClient, *numRange)
	if err != nil {
		log.Fatalf("Failed to fetch mails: %v", err)
	}

	displayMailInfo(mails)
}

func parseServer(server string) (string, string) {
	host, port, err := net.SplitHostPort(server)
	if err != nil {
		return server, "143"
	}
	return host, port
}

func getPassword() string {
	fmt.Print("Enter your password: ")
	password, _ := readPassword()
	fmt.Println()
	return string(password)
}

func connect(host, port string, ssl bool, user, password string) (*client.Client, error) {
	addr := fmt.Sprintf("%s:%s", host, port)
	var c *client.Client
	var err error

	if ssl {
		c, err = client.DialTLS(addr, nil)
	} else {
		c, err = client.Dial(addr)
	}
	if err != nil {
		return nil, err
	}

	err = c.Login(user, password)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func fetchMails(c *client.Client, numRange string) ([]mailInfo, error) {
	mbox, err := c.Select("INBOX", false)
	if err != nil {
		return nil, err
	}

	var seqSet *imap.SeqSet
	if numRange == "" {
		seqSet = new(imap.SeqSet)
		seqSet.AddRange(1, mbox.Messages)
	} else {
		seqSet, err = imap.ParseSeqSet(numRange)
		if err != nil {
			return nil, err
		}
	}

	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqSet, []imap.FetchItem{imap.FetchEnvelope, imap.FetchRFC822Size, imap.FetchBodyStructure}, messages)
	}()

	var mails []mailInfo
	for msg := range messages {
		m := mailInfo{}
		m.From = msg.Envelope.From[0].Address()
		m.To = msg.Envelope.To[0].Address()
		m.Subject = msg.Envelope.Subject
		m.Date = msg.Envelope.Date.Format(time.RFC1123)
		m.Size = int(msg.Size)

		attachments := getAttachments(msg.BodyStructure)
		m.Attachments = len(attachments)
		m.AttachmentNames = make([]string, 0, len(attachments))
		for _, att := range attachments {
			m.AttachmentNames = append(m.AttachmentNames, att.filename)
		}

		mails = append(mails, m)
	}

	if err := <-done; err != nil {
		return nil, err
	}
	return mails, nil
}

type attachmentInfo struct {
	filename string
	size     int
}

func getAttachments(bs *imap.BodyStructure) []attachmentInfo {
	if bs.MIMEType == "multipart" {
		var attachments []attachmentInfo
		for _, part := range bs.Parts {
			attachments = append(attachments, getAttachments(part)...)
		}
		return attachments
	}

	if bs.Disposition != "" && strings.ToLower(bs.Disposition) == "attachment" {
		filename := ""
		if bs.Params != nil {
			filename = bs.Params["filename"]
		} else if bs.Params != nil {
			filename = bs.Params["name"]
		}

		if filename != "" {
			return []attachmentInfo{
				{filename: filename, size: int(bs.Size)},
			}
		}
	}

	return []attachmentInfo{}
}

func displayMailInfo(mails []mailInfo) {
	fmt.Println("To Whom\tFrom Whom\tSubject\tDate\tLetter Size\tAttachments\tAttachment Names")
	for _, m := range mails {
		fmt.Printf("%s\t%s\t%s\t%s\t%d\t%d\t%s\n", m.To, m.From, m.Subject, m.Date, m.Size, m.Attachments, strings.Join(m.AttachmentNames, ", "))
	}
}

func readPassword() ([]byte, error) {
	t, err := term.Open("/dev/tty")
	if err != nil {
		return nil, err
	}

	err = term.RawMode(t)
	if err != nil {
		t.Close()
		return nil, err
	}

	buf := new(bytes.Buffer)
	reader := bufio.NewReader(t)

	for {
		ch, _, err := reader.ReadRune()
		if err != nil {
			t.Restore()
			t.Close()
			return nil, err
		}
		if ch == '\r' || ch == '\n' {
			break
		}
		buf.WriteRune(ch)
	}

	err = t.Restore()
	if err != nil {
		t.Close()
		return nil, err
	}

	err = t.Close()
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
