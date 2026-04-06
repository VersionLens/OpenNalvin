package cmd

import (
	"fmt"

	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/output"
	"github.com/spf13/cobra"
)

var kbNodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "Manage knowledge graph nodes",
}

var (
	kbNodesListQuery string
	kbNodesListKind  string
	kbNodesListLimit int

	kbNodesCreateKind       string
	kbNodesCreateName       string
	kbNodesCreateContent    string
	kbNodesCreateAttributes string

	kbNodesUpdateKind       string
	kbNodesUpdateName       string
	kbNodesUpdateContent    string
	kbNodesUpdateAttributes string

	kbNodesDeleteYes bool
)

var kbNodesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List knowledge nodes",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		nodes, err := store.ListNodes(cmd.Context(), kbNodesListQuery, kbNodesListKind, kbNodesListLimit)
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(nodes)
		}
		for _, node := range nodes {
			w.Line("%-36s  %-12s  %s", node.ID, node.Kind, node.Name)
		}
		return nil
	},
}

var kbNodesGetCmd = &cobra.Command{
	Use:   "get <node-id>",
	Short: "Get a knowledge node",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		node, err := store.GetNode(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(node)
		}
		w.Line("ID:         %s", node.ID)
		w.Line("Kind:       %s", node.Kind)
		w.Line("Name:       %s", node.Name)
		if node.Content != "" {
			w.Line("Content:    %s", node.Content)
		}
		if node.Attributes != "{}" {
			w.Line("Attributes: %s", node.Attributes)
		}
		w.Line("Created:    %s", node.CreatedAt.Format("2006-01-02 15:04:05"))
		w.Line("Updated:    %s", node.UpdatedAt.Format("2006-01-02 15:04:05"))
		return nil
	},
}

var kbNodesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a knowledge node",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		node, err := store.CreateNode(cmd.Context(), knowledge.NodeInput{
			Kind:       kbNodesCreateKind,
			Name:       kbNodesCreateName,
			Content:    kbNodesCreateContent,
			Attributes: kbNodesCreateAttributes,
		})
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(node)
		}
		w.Line("Created node %s", node.ID)
		return nil
	},
}

var kbNodesUpdateCmd = &cobra.Command{
	Use:   "update <node-id>",
	Short: "Update a knowledge node",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		node, err := store.UpdateNode(cmd.Context(), args[0], knowledge.NodeInput{
			Kind:       kbNodesUpdateKind,
			Name:       kbNodesUpdateName,
			Content:    kbNodesUpdateContent,
			Attributes: kbNodesUpdateAttributes,
		})
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(node)
		}
		w.Line("Updated node %s", node.ID)
		return nil
	},
}

var kbNodesDeleteCmd = &cobra.Command{
	Use:   "delete <node-id>",
	Short: "Delete a knowledge node",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !kbNodesDeleteYes {
			return fmt.Errorf("pass --yes to confirm deletion")
		}

		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		if err := store.DeleteNode(cmd.Context(), args[0]); err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if !w.IsJSON() {
			w.Line("Deleted node %s", args[0])
		}
		return nil
	},
}

func init() {
	kbCmd.AddCommand(kbNodesCmd)

	kbNodesListCmd.Flags().StringVarP(&kbNodesListQuery, "query", "q", "", "search query")
	kbNodesListCmd.Flags().StringVarP(&kbNodesListKind, "kind", "k", "", "filter by node kind")
	kbNodesListCmd.Flags().IntVarP(&kbNodesListLimit, "limit", "n", 50, "max results")
	kbNodesCmd.AddCommand(kbNodesListCmd)

	kbNodesCmd.AddCommand(kbNodesGetCmd)

	kbNodesCreateCmd.Flags().StringVar(&kbNodesCreateKind, "kind", "", "node kind (required)")
	kbNodesCreateCmd.Flags().StringVar(&kbNodesCreateName, "name", "", "node name (required)")
	kbNodesCreateCmd.Flags().StringVar(&kbNodesCreateContent, "content", "", "node content")
	kbNodesCreateCmd.Flags().StringVar(&kbNodesCreateAttributes, "attributes", "", "JSON attributes")
	_ = kbNodesCreateCmd.MarkFlagRequired("kind")
	_ = kbNodesCreateCmd.MarkFlagRequired("name")
	kbNodesCmd.AddCommand(kbNodesCreateCmd)

	kbNodesUpdateCmd.Flags().StringVar(&kbNodesUpdateKind, "kind", "", "node kind")
	kbNodesUpdateCmd.Flags().StringVar(&kbNodesUpdateName, "name", "", "node name")
	kbNodesUpdateCmd.Flags().StringVar(&kbNodesUpdateContent, "content", "", "node content")
	kbNodesUpdateCmd.Flags().StringVar(&kbNodesUpdateAttributes, "attributes", "", "JSON attributes")
	kbNodesCmd.AddCommand(kbNodesUpdateCmd)

	kbNodesDeleteCmd.Flags().BoolVar(&kbNodesDeleteYes, "yes", false, "confirm deletion")
	kbNodesCmd.AddCommand(kbNodesDeleteCmd)
}
