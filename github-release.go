package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type release struct {
	TagName      string `json:"tag_name"`
	TargetBranch string `json:"target_commitish"`
	ReleaseName  string `json:"name"`
	Body         string `json:"body"`
	PreRelease   bool   `json:"prerelease"`
}

type userInputs struct {
	tag               string
	releaseName       string
	previousTag       string
	preRelease        bool
	projects          []string
	user              string
	source            string
	fallbackBranch    string
	timeout           int
	supportBranchName string
}

/* {
	"ref": "refs/tags/4.29.1",
	"node_id": "",
	"url": "https://api.github.com/repos/comdotlinux/java-design-patterns/git/refs/tags/4.29.1",
	"object": {
	  "sha": "1b323c26943a32bc83832cc78d7ceb36e4f504e8",
	  "type": "commit",
	  "url": "https://api.github.com/repos/comdotlinux/java-design-patterns/git/commits/1b323c26943a32bc83832cc78d7ceb36e4f504e8"
	}
} */
type referenceResponse struct {
	Ref    string            `json:"ref"`
	NodeID string            `json:"node_id"`
	URL    string            `json:"url"`
	Object objectInReference `json:"object"`
}

type objectInReference struct {
	Sha      string `json:"sha"`
	TypeInfo string `json:"type"`
	URL      string `json:"url"`
}

/*
https://api.github.com/repos/<AUTHOR>/<REPO>/git/refs
{
    "ref": "refs/heads/<NEW-BRANCH-NAME>",
    "sha": "<HASH-TO-BRANCH-FROM>"
}
*/
type createBranchBody struct {
	Ref string `json:"ref"`
	Sha string `json:"sha"`
}

const environmentTokenKey = "OAUTH_TOKEN"
const dockerNamesURL = "https://frightanic.com/goodies_content/docker-names.php"
const apiBaseURL = "https://api.github.com/repos"
const githubURL = "https://github.com/%s/%s/compare/%s...%s"

func main() {
	if token, present := os.LookupEnv(environmentTokenKey); !present || token == "" {
		log.Fatalf("Please set a environment variable named %s created on github.", environmentTokenKey)
	}

	var userInput userInputs
	flag.StringVar(&userInput.user, "user", "idnowgmbh", "The User / Owner of the repository")
	flag.StringVar(&userInput.source, "source", "master", "The source branch/tag to create the new tag")
	flag.StringVar(&userInput.fallbackBranch, "fallback-branch", "master", "The fallback branch to create the TAG on if the source branch does not exist in the repository.")
	flag.IntVar(&userInput.timeout, "timeout", 5, "The Timeout for Github API Calls")
	flag.StringVar(&userInput.supportBranchName, "support-branch-name", "", "The name of the support branch to create if source branch is a tag")

	flag.StringVar(&userInput.tag, "tag", "", "The tag to create.")
	flag.StringVar(&userInput.releaseName, "release-name", "", "The name of the Release")
	flag.StringVar(&userInput.previousTag, "previous-tag", "", "The previous tag to use in the message")
	flag.BoolVar(&userInput.preRelease, "pre-release", true, "If this is a pre-release, use -pre-release=false to change")

	flag.Usage = usage
	flag.Parse()

	userInput.projects = flag.Args()

	inputValidaton(userInput)
	userInput.releaseName = getReleaseName(userInput)

	client := &http.Client{
		Timeout: time.Duration(time.Second * time.Duration(userInput.timeout)),
	}

	for index, project := range userInput.projects {
		log.Printf("%2d : Starting Release %s for %s with Tag Version %s on branch %s with fallback branch %s and possible support branch %s", index+1, userInput.releaseName, project, userInput.tag, userInput.source, userInput.fallbackBranch, userInput.supportBranchName)

		projectAPIBaseURL := fmt.Sprintf("%s/%s/%s", apiBaseURL, userInput.user, project)
		targetBranch, err := checkBranch(client, userInput, project, projectAPIBaseURL)
		if err != nil {
			log.Fatalf("Could not get the Target Branch %v", err)
		}
		log.Printf("Selected Branch %s to create tag %s", targetBranch, userInput.tag)
		createRelease(client, userInput, targetBranch, project, projectAPIBaseURL)
	}
}

func usage() {
	executableName := os.Args[0]
	fmt.Fprintf(flag.CommandLine.Output(), "\n%s is an opinionated implementation of some Github APIs that can be used to create release tags for multiple projects\n", executableName)
	fmt.Fprintf(flag.CommandLine.Output(), "\nUsage: %s -user comdotlinux -source master -tag v0.0.2 -previous-tag v0.0.1 java-design-patterns TasteOfJavaEE7", executableName)
	fmt.Fprintf(flag.CommandLine.Output(), "\nUsage: %s -user comdotlinux -source support/v0.0.x -tag v0.0.3 -fallback-branch master -previous-tag v0.0.1 -release-name Duke -pre-release=false java-design-patterns TasteOfJavaEE7", executableName)
	fmt.Fprintf(flag.CommandLine.Output(), "\nUsage: %s -user comdotlinux -source v.0.0.1 -tag v0.0.2-RC.1 -support-branch-name support/v0.0.x -previous-tag v0.0.1 java-design-patterns TasteOfJavaEE7", executableName)
	fmt.Fprintf(flag.CommandLine.Output(), "\nWhen -source is a TAG -support-branch-name is mandatory. \n")
	fmt.Fprintf(flag.CommandLine.Output(), "An environment variable with the name %s is mandatory for all actions!\nSee https://developer.github.com/v3/#oauth2-token-sent-in-a-header to get one.\n\n", environmentTokenKey)
	fmt.Fprintf(flag.CommandLine.Output(), "Below are the possible parameters:\n")
	flag.PrintDefaults()
	os.Exit(3)
}

func checkBranch(client *http.Client, userInput userInputs, project string, projectAPIBaseURL string) (string, error) {
	url := fmt.Sprintf("%s/branches/%s", projectAPIBaseURL, userInput.source)
	res, err := doGet(client, url)
	if err != nil {
		log.Fatalf("Received error %v", err)
	}

	if statusSuccess(res.StatusCode) {
		log.Printf("Branch %s looks good, will be selected. Response : %v", userInput.source, http.StatusText(res.StatusCode))
		return userInput.source, nil
	}

	if res.StatusCode == http.StatusNotFound {
		log.Printf("Checking if source %s is a TAG", userInput.source)
		url = fmt.Sprintf("%s/releases/tags/%s", projectAPIBaseURL, userInput.source)
		res, err := doGet(client, url)
		if err != nil {
			log.Fatalf("Could not Check release tags : %v", err)
		}

		if statusSuccess(res.StatusCode) {
			log.Printf("%s is a Tag", userInput.source)
			if userInput.supportBranchName == "" {
				log.Fatalf("If Source is a tag, name of support branch to create from this is necessary!")
			}
			log.Printf("Since %s is a Tag, Creating support branch %s", userInput.source, userInput.supportBranchName)

			url = fmt.Sprintf("%s/git/refs/tags/%s", projectAPIBaseURL, userInput.source)
			res, err := doGet(client, url)
			if err != nil {
				log.Fatalf("Could not get Tag %s commit userInput. : %v", userInput.source, err)
			}

			if statusSuccess(res.StatusCode) {
				log.Println("Reading response body to get commit hash")
				defer res.Body.Close()
				tagInfoResponseBody, errorReadingBody := ioutil.ReadAll(res.Body)
				if errorReadingBody != nil {
					log.Fatalf("Could not read Response body, cannot proceed : %v", errorReadingBody)
				}

				log.Println("Read Response body, trying to unmarshal the Json")
				var tagReference referenceResponse
				if err := json.Unmarshal(tagInfoResponseBody, &tagReference); err != nil {
					log.Fatalf("Could not read response for tag info to get sha commit %v", err)
				}

				log.Printf("Response body read into referenceResponse, sha hash : %s", tagReference.Object.Sha)

				createBranch := createBranchBody{
					Ref: fmt.Sprintf("refs/heads/%s", userInput.supportBranchName),
					Sha: tagReference.Object.Sha,
				}

				log.Printf("Created Request Object to create branch : %v", createBranch)
				bodyBytes, err := json.Marshal(createBranch)
				if err != nil {
					log.Fatalf("Could not create body for creating branch. %v", err)
				}

				log.Println("structconverted to Json Bytes")
				url = fmt.Sprintf("%s/git/refs", projectAPIBaseURL)
				log.Printf("Calling URL %s to create Branch %s", url, createBranch.Ref)
				req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(bodyBytes))
				if err != nil {
					log.Fatalf("Could not create Request for creating branch. %v", err)
				}

				addAuthAndAcceptHeader(req)
				req.Header.Add(http.CanonicalHeaderKey("Content-Type"), "application/json")
				res, err := client.Do(req)
				if err != nil {
					log.Fatalf("Error in creating the branch : %v", err)
				}
				log.Printf("POST call to create branch completed with status %s", http.StatusText(res.StatusCode))
				if res.StatusCode == http.StatusCreated {
					defer res.Body.Close()
					createBranchResponseBody, errorReadingBody := ioutil.ReadAll(res.Body)
					if errorReadingBody != nil {
						log.Fatalf("Could not read Response body, cannot proceed : %v", errorReadingBody)
					}

					var branchReference referenceResponse
					if err := json.Unmarshal(createBranchResponseBody, &branchReference); err != nil {
						log.Fatalf("Could not read response for tag info to get sha commit %v", err)
					}

					log.Printf("Created Branch with Details : %v", branchReference)

					branchNameArray := strings.SplitN(branchReference.Ref, "/", 3)
					if len(branchNameArray) == 3 {
						return branchNameArray[2], nil
					}
					return branchReference.Ref, nil
				}
				log.Printf("POST call to create branch completed with response %v", res)
			}
		} else {
			log.Printf("Use fallback branch since %s is neither a branch nor a Tag", userInput.source)
			url := fmt.Sprintf("%s/branches/%s", projectAPIBaseURL, userInput.fallbackBranch)
			res, err := doGet(client, url)
			if err != nil {
				log.Fatalf("Received error %v", err)
			}

			if statusSuccess(res.StatusCode) {
				log.Printf("Branch %s looks good, will be selected. Response : %v", userInput.fallbackBranch, http.StatusText(res.StatusCode))
				return userInput.fallbackBranch, nil
			}
		}
	}

	log.Println("The source branch parameter and the fallback cannot be used to create release, so using master")
	return "master", nil
}

func statusSuccess(statusCode int) bool {
	log.Printf("checking status code %d", statusCode)
	return statusCode >= http.StatusOK && statusCode <= 299
}

func doGet(client *http.Client, url string) (*http.Response, error) {
	log.Printf("Calling URL %s", url)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	addAuthAndAcceptHeader(req)

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	log.Printf("Response %v", res)
	return res, nil
}

func addAuthAndAcceptHeader(request *http.Request) {
	request.Header.Add(http.CanonicalHeaderKey("Authorization"), fmt.Sprintf("token %s", os.Getenv(environmentTokenKey)))
	request.Header.Add(http.CanonicalHeaderKey("Accept"), "application/vnd.github.v3+json")
}

func createRelease(client *http.Client, userInput userInputs, targetBranch string, project string, projectAPIBaseURL string) {
	previousComparePoint := targetBranch
	if !isEmpty(userInput.previousTag) {
		previousComparePoint = userInput.previousTag
	}
	releaseCompareBody := fmt.Sprintf(githubURL, userInput.user, project, previousComparePoint, userInput.tag)
	releaseRequest := release{
		TagName:      userInput.tag,
		ReleaseName:  userInput.releaseName,
		PreRelease:   userInput.preRelease,
		TargetBranch: targetBranch,
		Body:         releaseCompareBody,
	}

	b, _ := json.MarshalIndent(releaseRequest, "", "    ")
	os.Stdout.Write(b)

	url := fmt.Sprintf("%s/releases", projectAPIBaseURL)
	log.Printf("Calling URL %s to create Release %v", url, releaseRequest)
	bodyBytes, err := json.Marshal(releaseRequest)
	if err != nil {
		log.Fatalf("Could not create body for creating release. %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Fatalf("Could not create Body Json Bytes, from release object. %v", err)
	}

	addAuthAndAcceptHeader(req)
	req.Header.Add(http.CanonicalHeaderKey("Content-Type"), "application/json")
	res, err := client.Do(req)
	if err != nil {
		log.Fatalf("Create release Failed, %v", err)
	}

	if !statusSuccess(res.StatusCode) {
		log.Fatalf("Create release Failed with status, %d : %s", res.StatusCode, http.StatusText(res.StatusCode))
	}

	log.Printf("Release Tag Created : %v", res)
}

func getReleaseName(userInput userInputs) string {
	releaseName := userInput.releaseName
	if isEmpty(userInput.releaseName) {
		log.Printf("Since the release name is unavailable, getting a random release name using %s", dockerNamesURL)
		releaseName = getRandomReleaseName()
		if isEmpty(releaseName) {
			releaseName = "Release of " + userInput.tag
			log.Printf("Since getting release name was not possible, using %s as release name", releaseName)
		}
	}
	return releaseName
}

func getRandomReleaseName() string {
	client := http.Client{}
	resp, err := client.Get(dockerNamesURL)
	if err != nil {
		log.Printf("Unable to get Docker Container Names for random release name. %v", err)
		return ""
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading body for the release name : %v", err)
		return ""
	}

	return strings.TrimRight(fmt.Sprintf("%s", body), "\n")
}

func isEmpty(input string) bool {
	return len(input) == 0 || input == ""
}

func inputValidaton(userInput userInputs) {

	var errors []string

	if isEmpty(userInput.user) {
		errors = append(errors, "User / Organization parameter is mandatory")
	}

	if isEmpty(userInput.source) {
		errors = append(errors, "source parameter is mandatory and must either be a branch OR a existing TAG on Github")
	}

	if isEmpty(userInput.tag) {
		errors = append(errors, "tag parameter is mandatory, otherwise what are we releasing?")
	}

	if len(userInput.projects) == 0 {
		errors = append(errors, "Atleast provide one project, otherwise where do we create the tag?")
	}

	if len(errors) != 0 {
		fmt.Fprintln(flag.CommandLine.Output(), "")
		for index, err := range errors {
			fmt.Fprintf(flag.CommandLine.Output(), "%2d : %s\n", index+1, err)
		}
		fmt.Fprintln(flag.CommandLine.Output(), "")
		flag.Usage()
	}

}
