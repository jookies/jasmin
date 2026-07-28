import { Create, useForm } from "@refinedev/antd";
import { RouteFields } from "./form";

export const RouteCreate = () => {
  const { formProps, saveButtonProps } = useForm();
  return (
    <Create saveButtonProps={saveButtonProps}>
      <RouteFields formProps={formProps} />
    </Create>
  );
};
