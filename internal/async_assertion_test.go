package internal_test

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/net/context"
)

type FakeGinkgoSpecContext struct {
	Attached  func() string
	Cancelled bool
}

func (f *FakeGinkgoSpecContext) AttachProgressReporter(v func() string) func() {
	f.Attached = v
	return func() { f.Cancelled = true }
}

var _ = Describe("Asynchronous Assertions", func() {
	var ig *InstrumentedGomega
	BeforeEach(func() {
		ig = NewInstrumentedGomega()
	})

	Describe("Basic Eventually support", func() {
		Context("the positive case", func() {
			It("polls the function and matcher until a match occurs", func() {
				counter := 0
				ig.G.Eventually(func() string {
					counter++
					if counter > 5 {
						return MATCH
					}
					return NO_MATCH
				}).Should(SpecMatch())
				Ω(counter).Should(Equal(6))
				Ω(ig.FailureMessage).Should(BeZero())
			})

			It("continues polling even if the matcher errors", func() {
				counter := 0
				ig.G.Eventually(func() string {
					counter++
					if counter > 5 {
						return MATCH
					}
					return ERR_MATCH
				}).Should(SpecMatch())
				Ω(counter).Should(Equal(6))
				Ω(ig.FailureMessage).Should(BeZero())
			})

			It("times out eventually if the assertion doesn't match in time", func() {
				counter := 0
				ig.G.Eventually(func() string {
					counter++
					if counter > 100 {
						return MATCH
					}
					return NO_MATCH
				}).WithTimeout(200 * time.Millisecond).WithPolling(20 * time.Millisecond).Should(SpecMatch())
				Ω(counter).Should(BeNumerically(">", 2))
				Ω(counter).Should(BeNumerically("<", 20))
				Ω(ig.FailureMessage).Should(ContainSubstring("Timed out after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("positive: no match"))
				Ω(ig.FailureSkip).Should(Equal([]int{3}))
			})

			It("maps Within() correctly to timeout and polling intervals", func() {
				counter := 0
				ig.G.Eventually(func() bool {
					counter++
					return false
				}).WithTimeout(0).WithPolling(20 * time.Millisecond).Within(200 * time.Millisecond).Should(BeTrue())
				Ω(counter).Should(BeNumerically(">", 2))
				Ω(counter).Should(BeNumerically("<", 20))

				counter = 0
				ig.G.Eventually(func() bool {
					counter++
					return false
				}).WithTimeout(0).WithPolling(0). // first zero intervals, then set them
									Within(200 * time.Millisecond).ProbeEvery(20 * time.Millisecond).
									Should(BeTrue())
				Ω(counter).Should(BeNumerically(">", 2))
				Ω(counter).Should(BeNumerically("<", 20))
			})
		})

		Context("the negative case", func() {
			It("polls the function and matcher until a match does not occur", func() {
				counter := 0
				ig.G.Eventually(func() string {
					counter++
					if counter > 5 {
						return NO_MATCH
					}
					return MATCH
				}).ShouldNot(SpecMatch())
				Ω(counter).Should(Equal(6))
				Ω(ig.FailureMessage).Should(BeZero())
			})

			It("continues polling when the matcher errors - an error does not count as a successful non-match", func() {
				counter := 0
				ig.G.Eventually(func() string {
					counter++
					if counter > 5 {
						return NO_MATCH
					}
					return ERR_MATCH
				}).ShouldNot(SpecMatch())
				Ω(counter).Should(Equal(6))
				Ω(ig.FailureMessage).Should(BeZero())
			})

			It("times out eventually if the assertion doesn't match in time", func() {
				counter := 0
				ig.G.Eventually(func() string {
					counter++
					if counter > 100 {
						return NO_MATCH
					}
					return MATCH
				}).WithTimeout(200 * time.Millisecond).WithPolling(20 * time.Millisecond).ShouldNot(SpecMatch())
				Ω(counter).Should(BeNumerically(">", 2))
				Ω(counter).Should(BeNumerically("<", 20))
				Ω(ig.FailureMessage).Should(ContainSubstring("Timed out after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("negative: match"))
				Ω(ig.FailureSkip).Should(Equal([]int{3}))
			})
		})

		Context("when a failure occurs", func() {
			It("registers the appropriate helper functions", func() {
				ig.G.Eventually(NO_MATCH).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(SpecMatch())
				Ω(ig.FailureMessage).Should(ContainSubstring("Timed out after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("positive: no match"))
				Ω(ig.FailureSkip).Should(Equal([]int{3}))
				Ω(ig.RegisteredHelpers).Should(ContainElement("(*AsyncAssertion).Should"))
				Ω(ig.RegisteredHelpers).Should(ContainElement("(*AsyncAssertion).match"))
			})

			It("renders the matcher's error if an error occured", func() {
				ig.G.Eventually(ERR_MATCH).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(SpecMatch())
				Ω(ig.FailureMessage).Should(ContainSubstring("Timed out after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("Error: spec matcher error"))
			})

			It("renders the optional description", func() {
				ig.G.Eventually(NO_MATCH).WithTimeout(50*time.Millisecond).WithPolling(10*time.Millisecond).Should(SpecMatch(), "boop")
				Ω(ig.FailureMessage).Should(ContainSubstring("boop"))
			})

			It("formats and renders the optional description when there are multiple arguments", func() {
				ig.G.Eventually(NO_MATCH).WithTimeout(50*time.Millisecond).WithPolling(10*time.Millisecond).Should(SpecMatch(), "boop %d", 17)
				Ω(ig.FailureMessage).Should(ContainSubstring("boop 17"))
			})

			It("calls the optional description if it is a function", func() {
				ig.G.Eventually(NO_MATCH).WithTimeout(50*time.Millisecond).WithPolling(10*time.Millisecond).Should(SpecMatch(), func() string { return "boop" })
				Ω(ig.FailureMessage).Should(ContainSubstring("boop"))
			})
		})

		Context("when the passed-in context is cancelled", func() {
			It("stops and returns a failure", func() {
				ctx, cancel := context.WithCancel(context.Background())
				counter := 0
				ig.G.Eventually(func() string {
					counter++
					if counter == 2 {
						cancel()
					} else if counter == 10 {
						return MATCH
					}
					return NO_MATCH
				}, time.Hour, ctx).Should(SpecMatch())
				Ω(ig.FailureMessage).Should(ContainSubstring("Context was cancelled after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("positive: no match"))
			})

			It("can also be configured via WithContext()", func() {
				ctx, cancel := context.WithCancel(context.Background())
				counter := 0
				ig.G.Eventually(func() string {
					counter++
					if counter == 2 {
						cancel()
					} else if counter == 10 {
						return MATCH
					}
					return NO_MATCH
				}, time.Hour).WithContext(ctx).Should(SpecMatch())
				Ω(ig.FailureMessage).Should(ContainSubstring("Context was cancelled after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("positive: no match"))
			})

			It("counts as a failure for Consistently", func() {
				ctx, cancel := context.WithCancel(context.Background())
				counter := 0
				ig.G.Consistently(func() string {
					counter++
					if counter == 2 {
						cancel()
					} else if counter == 10 {
						return NO_MATCH
					}
					return MATCH
				}, time.Hour).WithContext(ctx).Should(SpecMatch())
				Ω(ig.FailureMessage).Should(ContainSubstring("Context was cancelled after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("positive: match"))
			})
		})

		Context("when the passed-in context is a Ginkgo SpecContext that can take a progress reporter attachment", func() {
			It("attaches a progress reporter context that allows it to report on demand", func() {
				fakeSpecContext := &FakeGinkgoSpecContext{}
				var message string
				ctx := context.WithValue(context.Background(), "GINKGO_SPEC_CONTEXT", fakeSpecContext)
				ig.G.Eventually(func() string {
					if fakeSpecContext.Attached != nil {
						message = fakeSpecContext.Attached()
					}
					return NO_MATCH
				}).WithTimeout(time.Millisecond * 20).WithContext(ctx).Should(Equal(MATCH))

				Ω(message).Should(Equal("Expected\n    <string>: no match\nto equal\n    <string>: match"))
				Ω(fakeSpecContext.Cancelled).Should(BeTrue())
			})
		})
	})

	Describe("Basic Consistently support", func() {
		Context("the positive case", func() {
			It("polls the function and matcher ensuring a match occurs consistently", func() {
				counter := 0
				ig.G.Consistently(func() string {
					counter++
					return MATCH
				}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(SpecMatch())
				Ω(counter).Should(BeNumerically(">", 1))
				Ω(counter).Should(BeNumerically("<", 7))
				Ω(ig.FailureMessage).Should(BeZero())
			})

			It("fails if the matcher ever errors", func() {
				counter := 0
				ig.G.Consistently(func() string {
					counter++
					if counter == 3 {
						return ERR_MATCH
					}
					return MATCH
				}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(SpecMatch())
				Ω(counter).Should(Equal(3))
				Ω(ig.FailureMessage).Should(ContainSubstring("Failed after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("Error: spec matcher error"))
			})

			It("fails if the matcher doesn't match at any point", func() {
				counter := 0
				ig.G.Consistently(func() string {
					counter++
					if counter == 3 {
						return NO_MATCH
					}
					return MATCH
				}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(SpecMatch())
				Ω(counter).Should(Equal(3))
				Ω(ig.FailureMessage).Should(ContainSubstring("Failed after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("positive: no match"))
			})
		})

		Context("the negative case", func() {
			It("polls the function and matcher ensuring a match never occurs", func() {
				counter := 0
				ig.G.Consistently(func() string {
					counter++
					return NO_MATCH
				}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(SpecMatch())
				Ω(counter).Should(BeNumerically(">", 1))
				Ω(counter).Should(BeNumerically("<", 7))
				Ω(ig.FailureMessage).Should(BeZero())
			})

			It("fails if the matcher ever errors", func() {
				counter := 0
				ig.G.Consistently(func() string {
					counter++
					if counter == 3 {
						return ERR_MATCH
					}
					return NO_MATCH
				}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(SpecMatch())
				Ω(counter).Should(Equal(3))
				Ω(ig.FailureMessage).Should(ContainSubstring("Failed after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("Error: spec matcher error"))
			})

			It("fails if the matcher matches at any point", func() {
				counter := 0
				ig.G.Consistently(func() string {
					counter++
					if counter == 3 {
						return MATCH
					}
					return NO_MATCH
				}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(SpecMatch())
				Ω(counter).Should(Equal(3))
				Ω(ig.FailureMessage).Should(ContainSubstring("Failed after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("negative: match"))
			})
		})

		Context("when a failure occurs", func() {
			It("registers the appropriate helper functions", func() {
				ig.G.Consistently(NO_MATCH).Should(SpecMatch())
				Ω(ig.FailureMessage).Should(ContainSubstring("Failed after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("positive: no match"))
				Ω(ig.FailureSkip).Should(Equal([]int{3}))
				Ω(ig.RegisteredHelpers).Should(ContainElement("(*AsyncAssertion).Should"))
				Ω(ig.RegisteredHelpers).Should(ContainElement("(*AsyncAssertion).match"))
			})

			It("renders the matcher's error if an error occured", func() {
				ig.G.Consistently(ERR_MATCH).Should(SpecMatch())
				Ω(ig.FailureMessage).Should(ContainSubstring("Failed after"))
				Ω(ig.FailureMessage).Should(ContainSubstring("Error: spec matcher error"))
			})

			It("renders the optional description", func() {
				ig.G.Consistently(NO_MATCH).Should(SpecMatch(), "boop")
				Ω(ig.FailureMessage).Should(ContainSubstring("boop"))
			})

			It("formats and renders the optional description when there are multiple arguments", func() {
				ig.G.Consistently(NO_MATCH).Should(SpecMatch(), "boop %d", 17)
				Ω(ig.FailureMessage).Should(ContainSubstring("boop 17"))
			})

			It("calls the optional description if it is a function", func() {
				ig.G.Consistently(NO_MATCH).Should(SpecMatch(), func() string { return "boop" })
				Ω(ig.FailureMessage).Should(ContainSubstring("boop"))
			})
		})
	})

	Describe("the passed-in actual", func() {
		type Foo struct{ Bar string }

		Context("when passed a value", func() {
			It("(eventually) continuously checks on the value until a match occurs", func() {
				c := make(chan bool)
				go func() {
					time.Sleep(100 * time.Millisecond)
					close(c)
				}()
				ig.G.Eventually(c).WithTimeout(1 * time.Second).WithPolling(10 * time.Millisecond).Should(BeClosed())
				Ω(ig.FailureMessage).Should(BeZero())
			})

			It("(consistently) continuously checks on the value ensuring a match always occurs", func() {
				c := make(chan bool)
				close(c)
				ig.G.Consistently(c).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(BeClosed())
				Ω(ig.FailureMessage).Should(BeZero())
			})
		})

		Context("when passed a function that takes no arguments and returns one value", func() {
			It("(eventually) polls the function until the returned value satisfies the matcher", func() {
				counter := 0
				ig.G.Eventually(func() int {
					counter += 1
					return counter
				}).WithTimeout(1 * time.Second).WithPolling(10 * time.Millisecond).Should(BeNumerically(">", 5))
				Ω(ig.FailureMessage).Should(BeZero())
			})

			It("(consistently) polls the function ensuring the returned value satisfies the matcher", func() {
				counter := 0
				ig.G.Consistently(func() int {
					counter += 1
					return counter
				}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(BeNumerically("<", 20))
				Ω(counter).Should(BeNumerically(">", 2))
				Ω(ig.FailureMessage).Should(BeZero())
			})

			It("works when the function returns nil", func() {
				counter := 0
				ig.G.Eventually(func() error {
					counter += 1
					if counter > 5 {
						return nil
					}
					return errors.New("oops")
				}).WithTimeout(1 * time.Second).WithPolling(10 * time.Millisecond).Should(BeNil())
				Ω(ig.FailureMessage).Should(BeZero())
			})
		})

		Context("when passed a function that takes no arguments and returns multiple values", func() {
			Context("with Eventually", func() {
				It("polls the function until the first returned value satisfies the matcher _and_ all additional values are zero", func() {
					counter, s, f, err := 0, "hi", Foo{Bar: "hi"}, errors.New("hi")
					ig.G.Eventually(func() (int, string, Foo, error) {
						switch counter += 1; counter {
						case 2:
							s = ""
						case 3:
							f = Foo{}
						case 4:
							err = nil
						}
						return counter, s, f, err
					}).WithTimeout(1 * time.Second).WithPolling(10 * time.Millisecond).Should(BeNumerically("<", 100))
					Ω(ig.FailureMessage).Should(BeZero())
					Ω(counter).Should(Equal(4))
				})

				It("reports on the non-zero value if it times out", func() {
					ig.G.Eventually(func() (int, string, Foo, error) {
						return 1, "", Foo{Bar: "hi"}, nil
					}).WithTimeout(30 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(BeNumerically("<", 100))
					Ω(ig.FailureMessage).Should(ContainSubstring("Error: Unexpected non-nil/non-zero argument at index 2:"))
					Ω(ig.FailureMessage).Should(ContainSubstring(`Foo{Bar:"hi"}`))
				})

				Context("when making a ShouldNot assertion", func() {
					It("doesn't succeed until the matcher is (not) satisfied with the first returned value _and_ all additional values are zero", func() {
						counter, s, f, err := 0, "hi", Foo{Bar: "hi"}, errors.New("hi")
						ig.G.Eventually(func() (int, string, Foo, error) {
							switch counter += 1; counter {
							case 2:
								s = ""
							case 3:
								f = Foo{}
							case 4:
								err = nil
							}
							return counter, s, f, err
						}).WithTimeout(1 * time.Second).WithPolling(10 * time.Millisecond).ShouldNot(BeNumerically("<", 0))
						Ω(ig.FailureMessage).Should(BeZero())
						Ω(counter).Should(Equal(4))
					})
				})
			})

			Context("with Consistently", func() {
				It("polls the function and succeeds if all the values are zero and the matcher is consistently satisfied", func() {
					var err error
					counter, s, f := 0, "", Foo{}
					ig.G.Consistently(func() (int, string, Foo, error) {
						counter += 1
						return counter, s, f, err
					}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(BeNumerically("<", 100))
					Ω(ig.FailureMessage).Should(BeZero())
					Ω(counter).Should(BeNumerically(">", 2))
				})

				It("polls the function and fails any of the values are non-zero", func() {
					var err error
					counter, s, f := 0, "", Foo{}
					ig.G.Consistently(func() (int, string, Foo, error) {
						counter += 1
						if counter == 3 {
							f = Foo{Bar: "welp"}
						}
						return counter, s, f, err
					}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(BeNumerically("<", 100))
					Ω(ig.FailureMessage).Should(ContainSubstring("Error: Unexpected non-nil/non-zero argument at index 2:"))
					Ω(ig.FailureMessage).Should(ContainSubstring(`Foo{Bar:"welp"}`))
					Ω(counter).Should(Equal(3))
				})

				Context("when making a ShouldNot assertion", func() {
					It("succeeds if all additional values are zero", func() {
						var err error
						counter, s, f := 0, "", Foo{}
						ig.G.Consistently(func() (int, string, Foo, error) {
							counter += 1
							return counter, s, f, err
						}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(BeNumerically(">", 100))
						Ω(ig.FailureMessage).Should(BeZero())
						Ω(counter).Should(BeNumerically(">", 2))
					})

					It("fails if any additional values are ever non-zero", func() {
						var err error
						counter, s, f := 0, "", Foo{}
						ig.G.Consistently(func() (int, string, Foo, error) {
							counter += 1
							if counter == 3 {
								s = "welp"
							}
							return counter, s, f, err
						}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(BeNumerically(">", 100))
						Ω(ig.FailureMessage).Should(ContainSubstring("Error: Unexpected non-nil/non-zero argument at index 1:"))
						Ω(ig.FailureMessage).Should(ContainSubstring(`<string>: "welp"`))
						Ω(counter).Should(Equal(3))
					})
				})
			})
		})

		Context("when passed a function that takes a Gomega argument and returns values", func() {
			Context("with Eventually", func() {
				It("passes in a Gomega and passes if the matcher matches, all extra values are zero, and there are no failed assertions", func() {
					counter, s, f, err := 0, "hi", Foo{Bar: "hi"}, errors.New("hi")
					ig.G.Eventually(func(g Gomega) (int, string, Foo, error) {
						switch counter += 1; counter {
						case 2:
							s = ""
						case 3:
							f = Foo{}
						case 4:
							err = nil
						}
						if counter == 5 {
							g.Expect(true).To(BeTrue())
						} else {
							g.Expect(false).To(BeTrue())
							panic("boom") //never see since the expectation stops execution
						}
						return counter, s, f, err
					}).WithTimeout(1 * time.Second).WithPolling(10 * time.Millisecond).Should(BeNumerically("<", 100))
					Ω(ig.FailureMessage).Should(BeZero())
					Ω(counter).Should(Equal(5))
				})

				It("times out if assertions in the function never succeed and reports on the error", func() {
					_, file, line, _ := runtime.Caller(0)
					ig.G.Eventually(func(g Gomega) int {
						g.Expect(false).To(BeTrue())
						return 10
					}).WithTimeout(30 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(Equal(10))
					Ω(ig.FailureMessage).Should(ContainSubstring("Error: Assertion in callback at %s:%d failed:", file, line+2))
					Ω(ig.FailureMessage).Should(ContainSubstring("Expected\n    <bool>: false\nto be true"))
				})

				It("forwards panics", func() {
					Ω(func() {
						ig.G.Eventually(func(g Gomega) int {
							g.Expect(true).To(BeTrue())
							panic("boom")
						}).WithTimeout(30 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(Equal(10))
					}).Should(PanicWith("boom"))
					Ω(ig.FailureMessage).Should(BeEmpty())
				})

				Context("when making a ShouldNot assertion", func() {
					It("doesn't succeed until all extra values are zero, there are no failed assertions, and the matcher is (not) satisfied", func() {
						counter, s, f, err := 0, "hi", Foo{Bar: "hi"}, errors.New("hi")
						ig.G.Eventually(func(g Gomega) (int, string, Foo, error) {
							switch counter += 1; counter {
							case 2:
								s = ""
							case 3:
								f = Foo{}
							case 4:
								err = nil
							}
							if counter == 5 {
								g.Expect(true).To(BeTrue())
							} else {
								g.Expect(false).To(BeTrue())
								panic("boom") //never see since the expectation stops execution
							}
							return counter, s, f, err
						}).WithTimeout(1 * time.Second).WithPolling(10 * time.Millisecond).ShouldNot(BeNumerically("<", 0))
						Ω(ig.FailureMessage).Should(BeZero())
						Ω(counter).Should(Equal(5))
					})
				})

				It("fails if an assertion is never satisfied", func() {
					_, file, line, _ := runtime.Caller(0)
					ig.G.Eventually(func(g Gomega) int {
						g.Expect(false).To(BeTrue())
						return 9
					}).WithTimeout(30 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(Equal(10))
					Ω(ig.FailureMessage).Should(ContainSubstring("Error: Assertion in callback at %s:%d failed:", file, line+2))
					Ω(ig.FailureMessage).Should(ContainSubstring("Expected\n    <bool>: false\nto be true"))
				})
			})

			Context("with Consistently", func() {
				It("passes in a Gomega and passes if the matcher matches, all extra values are zero, and there are no failed assertions", func() {
					var err error
					counter, s, f := 0, "", Foo{}
					ig.G.Consistently(func(g Gomega) (int, string, Foo, error) {
						counter += 1
						g.Expect(true).To(BeTrue())
						return counter, s, f, err
					}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(BeNumerically("<", 100))
					Ω(ig.FailureMessage).Should(BeZero())
					Ω(counter).Should(BeNumerically(">", 2))
				})

				It("fails if the passed-in gomega ever hits a failure", func() {
					var err error
					counter, s, f := 0, "", Foo{}
					_, file, line, _ := runtime.Caller(0)
					ig.G.Consistently(func(g Gomega) (int, string, Foo, error) {
						counter += 1
						g.Expect(true).To(BeTrue())
						if counter == 3 {
							g.Expect(false).To(BeTrue())
							panic("boom") //never see this
						}
						return counter, s, f, err
					}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(BeNumerically("<", 100))
					Ω(ig.FailureMessage).Should(ContainSubstring("Error: Assertion in callback at %s:%d failed:", file, line+5))
					Ω(ig.FailureMessage).Should(ContainSubstring("Expected\n    <bool>: false\nto be true"))
					Ω(counter).Should(Equal(3))
				})

				It("forwards panics", func() {
					Ω(func() {
						ig.G.Consistently(func(g Gomega) int {
							g.Expect(true).To(BeTrue())
							panic("boom")
						}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(Equal(10))
					}).Should(PanicWith("boom"))
					Ω(ig.FailureMessage).Should(BeEmpty())
				})

				Context("when making a ShouldNot assertion", func() {
					It("succeeds if any interior assertions always pass", func() {
						ig.G.Consistently(func(g Gomega) int {
							g.Expect(true).To(BeTrue())
							return 9
						}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(Equal(10))
						Ω(ig.FailureMessage).Should(BeEmpty())
					})

					It("fails if any interior assertions ever fail", func() {
						counter := 0
						_, file, line, _ := runtime.Caller(0)
						ig.G.Consistently(func(g Gomega) int {
							g.Expect(true).To(BeTrue())
							counter += 1
							if counter == 3 {
								g.Expect(false).To(BeTrue())
								panic("boom") //never see this
							}
							return 9
						}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(Equal(10))
						Ω(ig.FailureMessage).Should(ContainSubstring("Error: Assertion in callback at %s:%d failed:", file, line+5))
						Ω(ig.FailureMessage).Should(ContainSubstring("Expected\n    <bool>: false\nto be true"))
					})
				})
			})
		})

		Context("when passed a function that takes a Gomega argument and returns nothing", func() {
			Context("with Eventually", func() {
				It("returns the first failed assertion as an error and so should Succeed() if the callback ever runs without issue", func() {
					counter := 0
					ig.G.Eventually(func(g Gomega) {
						counter += 1
						if counter < 5 {
							g.Expect(false).To(BeTrue())
							g.Expect("bloop").To(Equal("blarp"))
						}
					}).WithTimeout(1 * time.Second).WithPolling(10 * time.Millisecond).Should(Succeed())
					Ω(counter).Should(Equal(5))
					Ω(ig.FailureMessage).Should(BeZero())
				})

				It("returns the first failed assertion as an error and so should timeout if the callback always fails", func() {
					counter := 0
					ig.G.Eventually(func(g Gomega) {
						counter += 1
						if counter < 5000 {
							g.Expect(false).To(BeTrue())
							g.Expect("bloop").To(Equal("blarp"))
						}
					}).WithTimeout(100 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(Succeed())
					Ω(counter).Should(BeNumerically(">", 1))
					Ω(ig.FailureMessage).Should(ContainSubstring("Expected success, but got an error"))
					Ω(ig.FailureMessage).Should(ContainSubstring("<bool>: false"))
					Ω(ig.FailureMessage).Should(ContainSubstring("to be true"))
					Ω(ig.FailureMessage).ShouldNot(ContainSubstring("bloop"))
				})

				It("returns the first failed assertion as an error and should satisy ShouldNot(Succeed) eventually", func() {
					counter := 0
					ig.G.Eventually(func(g Gomega) {
						counter += 1
						if counter > 5 {
							g.Expect(false).To(BeTrue())
							g.Expect("bloop").To(Equal("blarp"))
						}
					}).WithTimeout(100 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(Succeed())
					Ω(counter).Should(Equal(6))
					Ω(ig.FailureMessage).Should(BeZero())
				})

				It("should fail to ShouldNot(Succeed) eventually if an error never occurs", func() {
					ig.G.Eventually(func(g Gomega) {
						g.Expect(true).To(BeTrue())
					}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(Succeed())
					Ω(ig.FailureMessage).Should(ContainSubstring("Timed out after"))
					Ω(ig.FailureMessage).Should(ContainSubstring("Expected failure, but got no error."))
				})
			})

			Context("with Consistently", func() {
				It("returns the first failed assertion as an error and so should Succeed() if the callback always runs without issue", func() {
					counter := 0
					ig.G.Consistently(func(g Gomega) {
						counter += 1
						g.Expect(true).To(BeTrue())
					}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(Succeed())
					Ω(counter).Should(BeNumerically(">", 2))
					Ω(ig.FailureMessage).Should(BeZero())
				})

				It("returns the first failed assertion as an error and so should fail if the callback ever fails", func() {
					counter := 0
					ig.G.Consistently(func(g Gomega) {
						counter += 1
						g.Expect(true).To(BeTrue())
						if counter == 3 {
							g.Expect(false).To(BeTrue())
							g.Expect("bloop").To(Equal("blarp"))
						}
					}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(Succeed())
					Ω(ig.FailureMessage).Should(ContainSubstring("Expected success, but got an error"))
					Ω(ig.FailureMessage).Should(ContainSubstring("<bool>: false"))
					Ω(ig.FailureMessage).Should(ContainSubstring("to be true"))
					Ω(ig.FailureMessage).ShouldNot(ContainSubstring("bloop"))
					Ω(counter).Should(Equal(3))
				})

				It("returns the first failed assertion as an error and should satisy ShouldNot(Succeed) consistently if an error always occur", func() {
					counter := 0
					ig.G.Consistently(func(g Gomega) {
						counter += 1
						g.Expect(true).To(BeFalse())
					}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(Succeed())
					Ω(counter).Should(BeNumerically(">", 2))
					Ω(ig.FailureMessage).Should(BeZero())
				})

				It("should fail to satisfy ShouldNot(Succeed) consistently if an error ever does not occur", func() {
					counter := 0
					ig.G.Consistently(func(g Gomega) {
						counter += 1
						if counter == 3 {
							g.Expect(true).To(BeTrue())
						} else {
							g.Expect(false).To(BeTrue())
						}
					}).WithTimeout(50 * time.Millisecond).WithPolling(10 * time.Millisecond).ShouldNot(Succeed())
					Ω(ig.FailureMessage).Should(ContainSubstring("Failed after"))
					Ω(ig.FailureMessage).Should(ContainSubstring("Expected failure, but got no error."))
					Ω(counter).Should(Equal(3))
				})
			})
		})

		Context("when passed a function that takes a context", func() {
			It("forwards its own configured context", func() {
				ctx := context.WithValue(context.Background(), "key", "value")
				Eventually(func(ctx context.Context) string {
					return ctx.Value("key").(string)
				}).WithContext(ctx).Should(Equal("value"))
			})

			It("forwards its own configured context _and_ a Gomega if requested", func() {
				ctx := context.WithValue(context.Background(), "key", "value")
				Eventually(func(g Gomega, ctx context.Context) {
					g.Expect(ctx.Value("key").(string)).To(Equal("schmalue"))
				}).WithContext(ctx).Should(MatchError(ContainSubstring("Expected\n    <string>: value\nto equal\n    <string>: schmalue")))
			})

			Context("when the assertion does not have an attached context", func() {
				It("errors", func() {
					ig.G.Eventually(func(ctx context.Context) string {
						return ctx.Value("key").(string)
					}).Should(Equal("value"))
					Ω(ig.FailureMessage).Should(ContainSubstring("The function passed to Eventually requested a context.Context, but no context has been provided.  Please pass one in using Eventually().WithContext()."))
					Ω(ig.FailureSkip).Should(Equal([]int{2}))
				})
			})
		})

		Context("when passed a function that takes additional arguments", func() {
			Context("with just arguments", func() {
				It("forwards those arguments along", func() {
					Eventually(func(a int, b string) string {
						return fmt.Sprintf("%d - %s", a, b)
					}).WithArguments(10, "four").Should(Equal("10 - four"))

					Eventually(func(a int, b string, c ...int) string {
						return fmt.Sprintf("%d - %s (%d%d%d)", a, b, c[0], c[1], c[2])
					}).WithArguments(10, "four", 5, 1, 0).Should(Equal("10 - four (510)"))
				})
			})

			Context("with a Gomega arugment as well", func() {
				It("can also forward arguments alongside a Gomega", func() {
					Eventually(func(g Gomega, a int, b int) {
						g.Expect(a).To(Equal(b))
					}).WithArguments(10, 3).ShouldNot(Succeed())
					Eventually(func(g Gomega, a int, b int) {
						g.Expect(a).To(Equal(b))
					}).WithArguments(3, 3).Should(Succeed())
				})
			})

			Context("with a context arugment as well", func() {
				It("can also forward arguments alongside a context", func() {
					ctx := context.WithValue(context.Background(), "key", "value")
					Eventually(func(ctx context.Context, animal string) string {
						return ctx.Value("key").(string) + " " + animal
					}).WithArguments("pony").WithContext(ctx).Should(Equal("value pony"))
				})
			})

			Context("with Gomega and context arugments", func() {
				It("forwards arguments alongside both", func() {
					ctx := context.WithValue(context.Background(), "key", "I have")
					f := func(g Gomega, ctx context.Context, count int, zoo ...string) {
						sentence := fmt.Sprintf("%s %d animals: %s", ctx.Value("key"), count, strings.Join(zoo, ", "))
						g.Expect(sentence).To(Equal("I have 3 animals: dog, cat, pony"))
					}

					Eventually(f).WithArguments(3, "dog", "cat", "pony").WithContext(ctx).Should(Succeed())
					Eventually(f).WithArguments(2, "dog", "cat").WithContext(ctx).Should(MatchError(ContainSubstring("Expected\n    <string>: I have 2 animals: dog, cat\nto equal\n    <string>: I have 3 animals: dog, cat, pony")))
				})
			})

			Context("with a context that is in the argument list", func() {
				It("does not forward the configured context", func() {
					ctxA := context.WithValue(context.Background(), "key", "A")
					ctxB := context.WithValue(context.Background(), "key", "B")

					Eventually(func(ctx context.Context, a string) string {
						return ctx.Value("key").(string) + " " + a
					}).WithContext(ctxA).WithArguments(ctxB, "C").Should(Equal("B C"))
				})
			})

			Context("and an incorrect number of arguments is provided", func() {
				It("errors", func() {
					ig.G.Eventually(func(a int) string {
						return ""
					}).Should(Equal("foo"))
					Ω(ig.FailureMessage).Should(ContainSubstring("The function passed to Eventually has signature func(int) string takes 1 arguments but 0 have been provided.  Please use Eventually().WithArguments() to pass the corect set of arguments."))

					ig.G.Eventually(func(a int, b int) string {
						return ""
					}).WithArguments(1).Should(Equal("foo"))
					Ω(ig.FailureMessage).Should(ContainSubstring("The function passed to Eventually has signature func(int, int) string takes 2 arguments but 1 has been provided.  Please use Eventually().WithArguments() to pass the corect set of arguments."))

					ig.G.Eventually(func(a int, b int) string {
						return ""
					}).WithArguments(1, 2, 3).Should(Equal("foo"))
					Ω(ig.FailureMessage).Should(ContainSubstring("The function passed to Eventually has signature func(int, int) string takes 2 arguments but 3 have been provided.  Please use Eventually().WithArguments() to pass the corect set of arguments."))

					ig.G.Eventually(func(g Gomega, a int, b int) string {
						return ""
					}).WithArguments(1, 2, 3).Should(Equal("foo"))
					Ω(ig.FailureMessage).Should(ContainSubstring("The function passed to Eventually has signature func(types.Gomega, int, int) string takes 3 arguments but 4 have been provided.  Please use Eventually().WithArguments() to pass the corect set of arguments."))

					ig.G.Eventually(func(a int, b int, c ...int) string {
						return ""
					}).WithArguments(1).Should(Equal("foo"))
					Ω(ig.FailureMessage).Should(ContainSubstring("The function passed to Eventually has signature func(int, int, ...int) string takes 3 arguments but 1 has been provided.  Please use Eventually().WithArguments() to pass the corect set of arguments."))

				})
			})
		})

		Describe("when passed an invalid function", func() {
			It("errors with a failure", func() {
				ig.G.Eventually(func() {}).Should(Equal("foo"))
				Ω(ig.FailureMessage).Should(ContainSubstring("The function passed to Eventually had an invalid signature of func()"))
				Ω(ig.FailureSkip).Should(Equal([]int{2}))

				ig.G.Consistently(func(ctx context.Context) {}).Should(Equal("foo"))
				Ω(ig.FailureMessage).Should(ContainSubstring("The function passed to Consistently had an invalid signature of func(context.Context)"))
				Ω(ig.FailureSkip).Should(Equal([]int{2}))

				ig.G.Eventually(func(ctx context.Context, g Gomega) {}).Should(Equal("foo"))
				Ω(ig.FailureMessage).Should(ContainSubstring("The function passed to Eventually had an invalid signature of func(context.Context, types.Gomega)"))
				Ω(ig.FailureSkip).Should(Equal([]int{2}))

				ig = NewInstrumentedGomega()
				ig.G.Eventually(func(foo string) {}).Should(Equal("foo"))
				Ω(ig.FailureMessage).Should(ContainSubstring("The function passed to Eventually had an invalid signature of func(string)"))
				Ω(ig.FailureSkip).Should(Equal([]int{2}))
			})
		})
	})

	Describe("Stopping Early", func() {
		Describe("when using OracleMatchers", func() {
			It("stops and gives up with an appropriate failure message if the OracleMatcher says things can't change", func() {
				c := make(chan bool)
				close(c)

				t := time.Now()
				ig.G.Eventually(c).WithTimeout(100*time.Millisecond).WithPolling(10*time.Millisecond).Should(Receive(), "Receive is an OracleMatcher that gives up if the channel is closed")
				Ω(time.Since(t)).Should(BeNumerically("<", 90*time.Millisecond))
				Ω(ig.FailureMessage).Should(ContainSubstring("No future change is possible."))
				Ω(ig.FailureMessage).Should(ContainSubstring("The channel is closed."))
			})

			It("never gives up if actual is a function", func() {
				c := make(chan bool)
				close(c)

				t := time.Now()
				ig.G.Eventually(func() chan bool { return c }).WithTimeout(100*time.Millisecond).WithPolling(10*time.Millisecond).Should(Receive(), "Receive is an OracleMatcher that gives up if the channel is closed")
				Ω(time.Since(t)).Should(BeNumerically(">=", 90*time.Millisecond))
				Ω(ig.FailureMessage).ShouldNot(ContainSubstring("No future change is possible."))
				Ω(ig.FailureMessage).Should(ContainSubstring("Timed out after"))
			})
		})

		Describe("The StopTrying signal", func() {
			Context("when success occurs on the last iteration", func() {
				It("succeeds and stops when the signal is returned", func() {
					possibilities := []string{"A", "B", "C"}
					i := 0
					Eventually(func() (string, error) {
						possibility := possibilities[i]
						i += 1
						if i == len(possibilities) {
							return possibility, StopTrying("Reached the end")
						} else {
							return possibility, nil
						}
					}).Should(Equal("C"))
					Ω(i).Should(Equal(3))
				})

				It("counts as success for consistently", func() {
					i := 0
					Consistently(func() (int, error) {
						i += 1
						if i >= 10 {
							return i, StopTrying("Reached the end")
						}
						return i, nil
					}).Should(BeNumerically("<=", 10))

					i = 0
					Consistently(func() int {
						i += 1
						if i >= 10 {
							StopTrying("Reached the end").Now()
						}
						return i
					}).Should(BeNumerically("<=", 10))
				})
			})

			Context("when success does not occur", func() {
				It("fails and stops trying early", func() {
					possibilities := []string{"A", "B", "C"}
					i := 0
					ig.G.Eventually(func() (string, error) {
						possibility := possibilities[i]
						i += 1
						if i == len(possibilities) {
							return possibility, StopTrying("Reached the end")
						} else {
							return possibility, nil
						}
					}).Should(Equal("D"))
					Ω(i).Should(Equal(3))
					Ω(ig.FailureMessage).Should(ContainSubstring("Reached the end - after"))
					Ω(ig.FailureMessage).Should(ContainSubstring("Expected\n    <string>: C\nto equal\n    <string>: D"))
				})
			})

			Context("when StopTrying().Now() is called", func() {
				It("halts execution, stops trying, and emits the last failure", func() {
					possibilities := []string{"A", "B", "C"}
					i := -1
					ig.G.Eventually(func() string {
						i += 1
						if i < len(possibilities) {
							return possibilities[i]
						} else {
							StopTrying("Out of tries").Now()
							panic("welp")
						}
					}).Should(Equal("D"))
					Ω(i).Should(Equal(3))
					Ω(ig.FailureMessage).Should(ContainSubstring("Out of tries - after"))
					Ω(ig.FailureMessage).Should(ContainSubstring("Expected\n    <string>: C\nto equal\n    <string>: D"))
				})
			})

			It("still allows regular panics to get through", func() {
				defer func() {
					e := recover()
					Ω(e).Should(Equal("welp"))
				}()
				Eventually(func() string {
					panic("welp")
				}).Should(Equal("A"))
			})

			Context("when used in conjunction wihth a Gomega and/or Context", func() {
				It("correctly catches the StopTrying signal", func() {
					i := 0
					ctx := context.WithValue(context.Background(), "key", "A")
					ig.G.Eventually(func(g Gomega, ctx context.Context, expected string) {
						i += 1
						if i >= 3 {
							StopTrying("Out of tries").Now()
						}
						g.Expect(ctx.Value("key")).To(Equal(expected))
					}).WithContext(ctx).WithArguments("B").Should(Succeed())
					Ω(i).Should(Equal(3))
					Ω(ig.FailureMessage).Should(ContainSubstring("Out of tries - after"))
					Ω(ig.FailureMessage).Should(ContainSubstring("Assertion in callback at"))
					Ω(ig.FailureMessage).Should(ContainSubstring("<string>: A"))
				})
			})
		})
	})

	When("vetting optional description parameters", func() {
		It("panics when Gomega matcher is at the beginning of optional description parameters", func() {
			ig := NewInstrumentedGomega()
			for _, expectator := range []string{
				"Should", "ShouldNot",
			} {
				Expect(func() {
					eventually := ig.G.Eventually(42) // sic!
					meth := reflect.ValueOf(eventually).MethodByName(expectator)
					Expect(meth.IsValid()).To(BeTrue())
					meth.Call([]reflect.Value{
						reflect.ValueOf(HaveLen(1)),
						reflect.ValueOf(ContainElement(42)),
					})
				}).To(PanicWith(MatchRegexp("Asynchronous assertion has a GomegaMatcher as the first element of optionalDescription")))
			}
		})

		It("accepts Gomega matchers in optional description parameters after the first", func() {
			Expect(func() {
				ig := NewInstrumentedGomega()
				ig.G.Eventually(42).Should(HaveLen(1), "foo", ContainElement(42))
			}).NotTo(Panic())
		})

	})

	Context("eventual nil-ism", func() { // issue #555
		It("doesn't panic on nil actual", func() {
			ig := NewInstrumentedGomega()
			Expect(func() {
				ig.G.Eventually(nil).Should(BeNil())
			}).NotTo(Panic())
		})

		It("doesn't panic on function returning nil error", func() {
			ig := NewInstrumentedGomega()
			Expect(func() {
				ig.G.Eventually(func() error { return nil }).Should(BeNil())
			}).NotTo(Panic())
		})
	})
})
